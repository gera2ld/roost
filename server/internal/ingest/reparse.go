package ingest

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"ganigeorgiev.com/pocketbase/pocketbase/core"

	"roost/server/internal/mailparse"
	"roost/server/internal/spool"
	"roost/server/internal/threading"
)

// ReparseResult counts what Reparse did.
type ReparseResult struct {
	Updated int
	Failed  int
}

const reparseBatch = 200

// Reparse re-reads every inbound message's raw EML and refreshes the fields the
// parser derives, plus its attachment rows. User state (read, starred, labels)
// and thread membership are kept; thread summaries are recomputed.
func Reparse(ctx context.Context, app core.App, mailDir string) (ReparseResult, error) {
	var res ReparseResult
	var errs []error
	threads := map[string]bool{}

	for offset := 0; ; offset += reparseBatch {
		batch, err := app.FindRecordsByFilter("messages", "direction = 'inbound' && raw_path != ''",
			"created", reparseBatch, offset)
		if err != nil {
			return res, err
		}
		for _, rec := range batch {
			if ctx.Err() != nil {
				return res, ctx.Err()
			}
			if err := reparseOne(app, mailDir, rec); err != nil {
				res.Failed++
				errs = append(errs, fmt.Errorf("%s: %w", rec.Id, err))
				continue
			}
			res.Updated++
			if t := rec.GetString("thread"); t != "" {
				threads[t] = true
			}
		}
		if len(batch) < reparseBatch {
			break
		}
	}
	for id := range threads {
		if err := threading.Recompute(app, id); err != nil {
			errs = append(errs, err)
		}
	}
	return res, errors.Join(errs...)
}

func reparseOne(app core.App, mailDir string, rec *core.Record) error {
	key := rec.GetString("raw_path")
	if !spool.ValidKey(key) {
		return fmt.Errorf("unexpected raw path %q", key)
	}
	raw, err := os.ReadFile(filepath.Join(mailDir, filepath.FromSlash(key)))
	if err != nil {
		return err
	}
	msg, err := mailparse.Parse(raw)
	if err != nil {
		return err
	}
	return app.RunInTransaction(func(tx core.App) error {
		applyParsed(rec, msg)
		if err := tx.Save(rec); err != nil {
			return err
		}
		return replaceAttachments(tx, rec.Id, msg.Attachments)
	})
}
