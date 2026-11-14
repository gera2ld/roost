package notify

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
)

// Defaults for Notifier.
const (
	DefaultWindow     = 3 * time.Second // gather a burst before sending
	DefaultIndividual = 3               // a burst up to this size sends one alert each
	queueSize         = 1000
	maxRetryAfter     = 30 * time.Second
)

// Notifier turns stored-message events into Telegram alerts. Enqueue never
// blocks ingest; a background loop batches bursts (a sweep catching up after
// downtime sends one summary, not a flood) and retries once on rate limits and
// network errors. Alerts are best effort: a failure is logged and dropped.
type Notifier struct {
	App        core.App
	Bot        Sender
	PublicURL  string
	Logger     *slog.Logger
	Window     time.Duration
	Individual int

	queue chan string
}

// New returns a Notifier ready for Enqueue and Run.
func New(app core.App, bot Sender, publicURL string, logger *slog.Logger) *Notifier {
	return &Notifier{App: app, Bot: bot, PublicURL: publicURL, Logger: logger,
		queue: make(chan string, queueSize)}
}

// Enqueue schedules an alert for a stored message id.
func (n *Notifier) Enqueue(messageID string) {
	select {
	case n.queue <- messageID:
	default:
		n.logger().Warn("notification queue full; alert dropped", "message", messageID)
	}
}

// Run delivers alerts until ctx ends.
func (n *Notifier) Run(ctx context.Context) {
	q := n.queue
	window := n.Window
	if window <= 0 {
		window = DefaultWindow
	}
	for {
		var ids []string
		select {
		case <-ctx.Done():
			return
		case id := <-q:
			ids = append(ids, id)
		}
		timer := time.NewTimer(window)
	gather:
		for {
			select {
			case id := <-q:
				ids = append(ids, id)
			case <-timer.C:
				break gather
			case <-ctx.Done():
				timer.Stop()
				return
			}
		}
		n.Flush(ctx, ids)
	}
}

// Flush sends the alerts for one burst of message ids.
func (n *Notifier) Flush(ctx context.Context, ids []string) {
	items := n.load(ids)
	if len(items) == 0 {
		return
	}
	individual := n.Individual
	if individual <= 0 {
		individual = DefaultIndividual
	}
	if len(items) <= individual {
		for _, it := range items {
			text, button := FormatOne(it, n.PublicURL)
			n.Post(ctx, text, button)
		}
		return
	}
	text, button := FormatBatch(items, n.PublicURL)
	n.Post(ctx, text, button)
}

func (n *Notifier) load(ids []string) []Item {
	var items []Item
	for _, id := range ids {
		m, err := n.App.FindRecordById("messages", id)
		if err != nil {
			continue // purged before the alert went out
		}
		items = append(items, Item{
			FromName:    m.GetString("from_name"),
			FromAddress: m.GetString("from_address"),
			Mailbox:     m.GetString("mailbox"),
			Subject:     m.GetString("subject"),
			Body:        m.GetString("text_body"),
			ThreadID:    m.GetString("thread"),
		})
	}
	return items
}

// Post sends one alert, retrying once on a rate limit or network error. It
// blocks for up to the retry wait; failures are logged, not returned.
func (n *Notifier) Post(ctx context.Context, text string, button *Button) {
	err := n.Bot.Send(ctx, text, button)
	if err == nil {
		return
	}
	wait := 2 * time.Second
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		if apiErr.RetryAfter <= 0 {
			// A 4xx other than rate limiting (bad token, chat, markup) won't
			// improve on retry.
			n.logger().Error("telegram alert failed", "error", err)
			return
		}
		wait = min(apiErr.RetryAfter, maxRetryAfter)
	}
	select {
	case <-ctx.Done():
		return
	case <-time.After(wait):
	}
	if err := n.Bot.Send(ctx, text, button); err != nil {
		n.logger().Error("telegram alert failed after retry", "error", err)
	}
}

func (n *Notifier) logger() *slog.Logger {
	if n.Logger != nil {
		return n.Logger
	}
	return slog.Default()
}

// WatchDelivery alerts when a sent message bounces or draws a spam complaint
// (delivery_status set by the Resend webhook). Sending happens in the
// background so the webhook answers at once.
func (n *Notifier) WatchDelivery(app core.App) {
	app.OnRecordAfterUpdateSuccess("messages").BindFunc(func(e *core.RecordEvent) error {
		status := e.Record.GetString("delivery_status")
		if (status == "bounced" || status == "complained") && e.Record.Original().GetString("delivery_status") != status {
			text, button := FormatDelivery(status, recipients(e.Record), e.Record.GetString("subject"),
				e.Record.GetString("send_error"), e.Record.GetString("thread"), n.PublicURL)
			go n.Post(context.Background(), text, button)
		}
		return e.Next()
	})
}

func recipients(rec *core.Record) string {
	var all []string
	for _, field := range []string{"to", "cc", "bcc"} {
		var list []struct {
			Address string `json:"address"`
		}
		_ = rec.UnmarshalJSONField(field, &list)
		for _, a := range list {
			all = append(all, a.Address)
		}
	}
	return strings.Join(all, ", ")
}
