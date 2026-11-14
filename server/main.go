package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"ganigeorgiev.com/pocketbase/pocketbase"
	"ganigeorgiev.com/pocketbase/pocketbase/apis"
	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/hook"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/osutils"
	"github.com/spf13/cobra"

	"roost/server/internal/api"
	"roost/server/internal/ingest"
	"roost/server/internal/monitor"
	"roost/server/internal/notify"
	"roost/server/internal/outbound"
	"roost/server/internal/spool"
	"roost/server/internal/store"
	"roost/server/internal/threading"
)

func main() {
	// ROOST_DATA_DIR (set in the image) makes every subcommand, not just serve, use the volume.
	app := pocketbase.NewWithConfig(pocketbase.Config{DefaultDataDir: os.Getenv("ROOST_DATA_DIR")})

	var mailDir string
	app.RootCmd.PersistentFlags().StringVar(&mailDir, "mailDir", envOr("ROOST_MAIL_DIR", "./mail"),
		"directory for write-once raw EML files (kept outside pb_data; back it up with the volume)")

	var publicDir string
	app.RootCmd.PersistentFlags().StringVar(&publicDir, "publicDir", defaultPublicDir(),
		"directory with the built web app (SvelteKit static output)")

	app.RootCmd.ParseFlags(os.Args[1:])

	app.RootCmd.AddCommand(schemaCommand(app))
	app.RootCmd.AddCommand(reparseCommand(app, &mailDir))
	app.RootCmd.AddCommand(importCommand(app, &mailDir))
	app.RootCmd.AddCommand(userCommand(app))
	app.RootCmd.AddCommand(notifyCommand())
	app.RootCmd.AddCommand(ingestCommand(app, &mailDir))

	threading.Register(app)

	app.OnServe().BindFunc(func(se *core.ServeEvent) error {
		// The embedded snapshot is the schema: applied in full on every start,
		// together with the search index that lives beside it.
		if err := store.Sync(se.App); err != nil {
			return err
		}
		if err := os.MkdirAll(mailDir, 0o750); err != nil {
			return err
		}

		se.Router.BindFunc(api.SecurityHeaders)
		se.Router.GET("/api/roost/health", func(e *core.RequestEvent) error {
			return e.JSON(http.StatusOK, map[string]string{"status": "ok"})
		})

		api.Register(se, mailDir, setupOutbound(se, mailDir))
		retention, err := envDays("ROOST_SPOOL_RETENTION_DAYS", 30)
		if err != nil {
			return err
		}
		se.App.Cron().MustAdd("roost_daily", "17 3 * * *", func() { daily(se.App, mailDir, retention) })

		alerts := setupNotify(se)
		mon, err := setupMonitor(se, alerts)
		if err != nil {
			return err
		}
		if err := setupIngest(se, mailDir, alerts, mon); err != nil {
			return err
		}
		return se.Next()
	})

	// Serve the PWA build; registered last so API routes win.
	app.OnServe().Bind(&hook.Handler[*core.ServeEvent]{
		Func: func(se *core.ServeEvent) error {
			if !se.Router.HasRoute(http.MethodGet, "/{path...}") {
				se.Router.GET("/{path...}", apis.Static(os.DirFS(publicDir), true))
			}
			return se.Next()
		},
		Priority: 999,
	})

	if err := app.Start(); err != nil {
		log.Fatal(err)
	}
}

// daily purges expired trash and trims ingest bookkeeping for spool objects
// that R2 has expired (a week of margin past the bucket's lifecycle rule).
func daily(app core.App, mailDir string, retention time.Duration) {
	logger := app.Logger()
	now := time.Now()
	n, err := ingest.PurgeExpiredTrash(app, mailDir, now)
	if err != nil {
		logger.Error("purge trash", "error", err, "purged", n)
	} else if n > 0 {
		logger.Info("purged expired trash", "threads", n)
	}
	trimmed, err := ingest.TrimKeys(app, now.Add(-retention-7*24*time.Hour), now)
	if err != nil {
		logger.Error("trim ingested keys", "error", err)
	} else if trimmed > 0 {
		logger.Info("trimmed ingested keys", "rows", trimmed)
	}
}

// setupMonitor schedules the pipeline health checks. Problems are logged, and
// sent to Telegram when it is configured.
func setupMonitor(se *core.ServeEvent, alerts *notify.Notifier) (*monitor.Monitor, error) {
	idle, err := envDays("ROOST_ALERT_IDLE_DAYS", 3)
	if err != nil {
		return nil, err
	}
	mon := &monitor.Monitor{App: se.App, Logger: se.App.Logger(), IdleAfter: idle}
	if alerts != nil {
		mon.Alert = func(ctx context.Context, html string) { alerts.Post(ctx, html, nil) }
	}
	se.App.Cron().MustAdd("roost_monitor", "7,37 * * * *", func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		mon.Run(ctx, time.Now())
	})
	return mon, nil
}

// setupOutbound returns the send service when Resend is configured, and mounts
// the delivery-event webhook when its signing secret is set.
func setupOutbound(se *core.ServeEvent, mailDir string) *outbound.Service {
	logger := se.App.Logger()
	if secret := os.Getenv("ROOST_RESEND_WEBHOOK_SECRET"); secret != "" {
		se.Router.POST("/api/roost/resend/webhook", outbound.WebhookHandler(secret))
	} else {
		logger.Warn("ROOST_RESEND_WEBHOOK_SECRET not set: delivery events are not recorded")
	}
	key := os.Getenv("ROOST_RESEND_API_KEY")
	if key == "" {
		logger.Warn("ROOST_RESEND_API_KEY not set: sending disabled (drafts still save)")
		return nil
	}
	resend := &outbound.Resend{APIKey: key, BaseURL: os.Getenv("ROOST_RESEND_BASE_URL")}
	return &outbound.Service{App: se.App, Sender: resend, MailDir: mailDir, Logger: logger}
}

// telegramFromEnv returns the bot client, or nil when Telegram is not configured.
func telegramFromEnv() *notify.Telegram {
	token, chat := os.Getenv("ROOST_TELEGRAM_BOT_TOKEN"), os.Getenv("ROOST_TELEGRAM_CHAT_ID")
	if token == "" || chat == "" {
		return nil
	}
	return &notify.Telegram{Token: token, ChatID: chat}
}

// setupNotify starts the Telegram alert loop when the bot is configured.
func setupNotify(se *core.ServeEvent) *notify.Notifier {
	bot := telegramFromEnv()
	if bot == nil {
		se.App.Logger().Warn("ROOST_TELEGRAM_BOT_TOKEN / ROOST_TELEGRAM_CHAT_ID not set: new-mail alerts off")
		return nil
	}
	n := notify.New(se.App, bot, os.Getenv("ROOST_PUBLIC_URL"), se.App.Logger())
	n.WatchDelivery(se.App)
	ctx, cancel := context.WithCancel(context.Background())
	se.App.OnTerminate().BindFunc(func(e *core.TerminateEvent) error {
		cancel()
		return e.Next()
	})
	go n.Run(ctx)
	return n
}

// setupIngest wires the nudge route and the periodic sweep when R2 is configured.
func setupIngest(se *core.ServeEvent, mailDir string, alerts *notify.Notifier, mon *monitor.Monitor) error {
	logger := se.App.Logger()
	r2, err := r2FromEnv()
	if err != nil {
		return err
	}
	if r2 == nil {
		logger.Warn("ROOST_R2_ENDPOINT not set: inbound ingest disabled")
		return nil
	}
	in := &ingest.Ingester{App: se.App, Spool: r2, MailDir: mailDir, Logger: logger}
	if alerts != nil {
		in.OnStored = alerts.Enqueue
	}
	mon.Ingester = in

	if secret := os.Getenv("ROOST_NUDGE_SECRET"); secret != "" {
		se.Router.POST("/api/roost/nudge", ingest.NudgeHandler(in, []byte(secret)))
	} else {
		logger.Warn("ROOST_NUDGE_SECRET not set: nudge route disabled, relying on the sweep")
	}

	sweep := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		res, err := in.Sweep(ctx)
		mon.SweepDone(err, time.Now())
		if err != nil {
			logger.Error("sweep finished with errors", "error", err, "result", res)
			return
		}
		if res.Stored+res.Dupes+res.Failed > 0 {
			logger.Info("sweep", "result", res)
		}
	}
	se.App.Cron().MustAdd("roost_sweep", "*/5 * * * *", sweep)
	go sweep() // catch up on anything that arrived while the server was down
	return nil
}

// r2FromEnv returns the spool client, or nil when R2 is not configured.
func r2FromEnv() (*spool.R2, error) {
	endpoint := os.Getenv("ROOST_R2_ENDPOINT")
	if endpoint == "" {
		return nil, nil
	}
	return spool.NewR2(endpoint, envOr("ROOST_R2_BUCKET", "roost-spool"),
		os.Getenv("ROOST_R2_ACCESS_KEY_ID"), os.Getenv("ROOST_R2_SECRET_ACCESS_KEY"))
}

// envDays reads a whole number of days; 0 is allowed (it disables a check).
func envDays(key string, fallback int) (time.Duration, error) {
	days := fallback
	if v := os.Getenv(key); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return 0, fmt.Errorf("%s: want a whole number of days, got %q", key, v)
		}
		days = n
	}
	return time.Duration(days) * 24 * time.Hour, nil
}

func schemaCommand(app *pocketbase.PocketBase) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "schema",
		Short: "Manage the embedded collections snapshot",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "export",
		Short: "Print the collections snapshot to embed as internal/store/schema.json",
		Long: "Print the database's collections in the canonical form the binary embeds.\n\n" +
			"Edit collections in the admin UI, run this, and commit the result. The snapshot\n" +
			"is applied in full on every start: what it omits is removed.\n\n" +
			"    go run . schema export > internal/store/schema.json",
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			// PocketBase bootstraps (opens the DB) before running commands.
			data, err := store.ExportSchema(app)
			if err != nil {
				return err
			}
			// Print, not Println: the export already ends in a newline.
			fmt.Print(string(data))
			return nil
		},
	})
	return cmd
}

func reparseCommand(app *pocketbase.PocketBase, mailDir *string) *cobra.Command {
	return &cobra.Command{
		Use:   "reparse",
		Short: "Re-read every stored raw message and refresh its parsed fields",
		Long: "Re-parse the raw EML of every inbound message with the current parser and\n" +
			"update subjects, addresses, bodies and attachment rows. Read state, stars,\n" +
			"labels and threads are kept. Run it after changing the MIME parser.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := store.Sync(app); err != nil {
				return err
			}
			res, err := ingest.Reparse(cmd.Context(), app, *mailDir)
			fmt.Printf("reparsed %d, failed %d\n", res.Updated, res.Failed)
			return err
		},
	}
}

func importCommand(app *pocketbase.PocketBase, mailDir *string) *cobra.Command {
	var to string
	cmd := &cobra.Command{
		Use:   "import PATH...",
		Short: "Ingest local .eml files or directories (dev data, migrating old mail)",
		Long: "Ingest .eml files through the normal ingest path. Keys come from the Date\n" +
			"header and a content hash, so importing the same file twice is a no-op.\n" +
			"The mailbox is the first To address unless --to is given.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := store.Sync(app); err != nil {
				return err
			}
			if err := os.MkdirAll(*mailDir, 0o750); err != nil {
				return err
			}
			res, err := ingest.ImportFiles(cmd.Context(), app, *mailDir, args, to)
			fmt.Printf("stored %d, duplicates %d, skipped %d, failed %d\n", res.Stored, res.Dupes, res.Skipped, res.Failed)
			return err
		},
	}
	cmd.Flags().StringVar(&to, "to", "", "receiving address to file the messages under")
	return cmd
}

func ingestCommand(app *pocketbase.PocketBase, mailDir *string) *cobra.Command {
	return &cobra.Command{
		Use:   "ingest KEY...",
		Short: "Ingest spooled R2 keys now, including ones that ran out of retries",
		Long: "Fetch and ingest the given R2 keys through the normal ingest path. Use it\n" +
			"after fixing whatever made a key fail (the monitor alert names the keys).\n" +
			"Keys already stored are skipped.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := store.Sync(app); err != nil {
				return err
			}
			r2, err := r2FromEnv()
			if err != nil {
				return err
			}
			if r2 == nil {
				return fmt.Errorf("set ROOST_R2_ENDPOINT and the R2 credentials")
			}
			if err := os.MkdirAll(*mailDir, 0o750); err != nil {
				return err
			}
			in := &ingest.Ingester{App: app, Spool: r2, MailDir: *mailDir}
			var failed int
			for _, key := range args {
				outcome, err := in.Ingest(cmd.Context(), key)
				if err != nil {
					failed++
					fmt.Printf("%s: %v\n", key, err)
					continue
				}
				fmt.Printf("%s: %s\n", key, outcome)
			}
			if failed > 0 {
				return fmt.Errorf("%d of %d keys failed", failed, len(args))
			}
			return nil
		},
	}
}

func notifyCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "notify", Short: "Telegram new-mail alerts"}
	cmd.AddCommand(&cobra.Command{
		Use:   "test",
		Short: "Send a test alert with the configured bot token and chat id",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			bot := telegramFromEnv()
			if bot == nil {
				return fmt.Errorf("set ROOST_TELEGRAM_BOT_TOKEN and ROOST_TELEGRAM_CHAT_ID")
			}
			text, button := notify.FormatOne(notify.Item{
				FromName: "Roost", FromAddress: "roost@localhost", Subject: "Test alert",
				Body: "Telegram alerts are working.",
			}, os.Getenv("ROOST_PUBLIC_URL"))
			if err := bot.Send(cmd.Context(), text, button); err != nil {
				return err
			}
			fmt.Println("sent")
			return nil
		},
	})
	return cmd
}

func userCommand(app *pocketbase.PocketBase) *cobra.Command {
	cmd := &cobra.Command{Use: "user", Short: "Manage the mail client's sign-in account"}
	cmd.AddCommand(&cobra.Command{
		Use:   "upsert EMAIL PASSWORD",
		Short: "Create the sign-in user, or reset its password",
		Args:  cobra.ExactArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			if err := store.Sync(app); err != nil {
				return err
			}
			user, err := app.FindAuthRecordByEmail("users", args[0])
			if err != nil {
				col, err := app.FindCollectionByNameOrId("users")
				if err != nil {
					return err
				}
				user = core.NewRecord(col)
				user.SetEmail(args[0])
				user.SetVerified(true)
			}
			user.SetPassword(args[1])
			if err := app.Save(user); err != nil {
				return err
			}
			fmt.Printf("user %s saved\n", args[0])
			return nil
		},
	})
	return cmd
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func defaultPublicDir() string {
	if osutils.IsProbablyGoRun() {
		return "./pb_public"
	}
	return filepath.Join(os.Args[0], "../pb_public")
}
