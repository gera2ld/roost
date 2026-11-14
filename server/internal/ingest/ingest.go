// Package ingest moves messages from the R2 spool into SQLite and the mail dir.
//
// Ingest is idempotent and serialized: nudges and the periodic sweep both call
// Ingest, and a key that was already handled is a cheap no-op. Dedupe happens on
// the R2 key first, then on Message-ID (the same message delivered to two
// catch-all addresses arrives as two objects).
package ingest

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"ganigeorgiev.com/pocketbase/pocketbase/core"

	"roost/server/internal/mailparse"
	"roost/server/internal/spool"
	"roost/server/internal/threading"
)

// Outcome of one Ingest call.
type Outcome string

const (
	Stored    Outcome = "stored"    // new message written
	Duplicate Outcome = "duplicate" // Message-ID already stored; recipient merged
	Skipped   Outcome = "skipped"   // key already handled earlier
)

// DefaultMaxSize bounds a single message (Cloudflare Email Routing caps at ~25 MiB).
const DefaultMaxSize = 30 << 20

// MaxAttempts is how many times the sweep retries a key that failed to ingest.
const MaxAttempts = 5

// ErrTooLarge marks a message over the size limit. Retrying cannot help, so the
// key is marked as out of attempts at once (and the monitor reports it).
var ErrTooLarge = errors.New("message too large")

// Ingester stores spooled messages. It is safe for concurrent use.
type Ingester struct {
	App     core.App
	Spool   spool.Spool
	MailDir string
	MaxSize int64
	Logger  *slog.Logger
	// OnStored, if set, is called with the record id of each newly stored
	// message after its transaction commits (not for duplicates or skips). It
	// must not block: notifications queue and send elsewhere.
	OnStored func(messageID string)

	mu sync.Mutex // serializes Ingest so dedupe checks and writes cannot race
}

// Ingest fetches, parses and stores the object at key.
func (in *Ingester) Ingest(ctx context.Context, key string) (Outcome, error) {
	if !spool.ValidKey(key) {
		return "", fmt.Errorf("invalid spool key %q", key)
	}
	in.mu.Lock()
	defer in.mu.Unlock()

	seen, err := in.findKey(key)
	if err != nil {
		return "", err
	}
	if seen != nil && seen.GetString("status") != "failed" {
		return Skipped, nil
	}

	outcome, storedID, err := in.ingest(ctx, key, seen)
	if err != nil {
		if recErr := in.recordFailure(key, seen, err); recErr != nil {
			err = errors.Join(err, recErr)
		}
		in.logger().Error("ingest failed", "key", key, "error", err)
		return "", err
	}
	in.logger().Info("ingested", "key", key, "outcome", outcome)
	if outcome == Stored && in.OnStored != nil {
		in.OnStored(storedID)
	}
	return outcome, nil
}

func (in *Ingester) ingest(ctx context.Context, key string, seen *core.Record) (Outcome, string, error) {
	body, obj, err := in.Spool.Get(ctx, key)
	if err != nil {
		return "", "", fmt.Errorf("fetch: %w", err)
	}
	defer body.Close()

	maxSize := in.MaxSize
	if maxSize <= 0 {
		maxSize = DefaultMaxSize
	}
	raw, err := io.ReadAll(io.LimitReader(body, maxSize+1))
	if err != nil {
		return "", "", fmt.Errorf("read: %w", err)
	}
	if int64(len(raw)) > maxSize {
		return "", "", fmt.Errorf("%w: over %d MiB", ErrTooLarge, maxSize>>20)
	}

	msg, err := mailparse.Parse(raw)
	if err != nil {
		return "", "", err
	}

	if msg.MessageID != "" {
		existing, err := in.App.FindFirstRecordByFilter("messages",
			"message_id = {:id} && direction = 'inbound'", map[string]any{"id": msg.MessageID})
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return "", "", err
		}
		if existing != nil {
			return Duplicate, "", in.App.RunInTransaction(func(tx core.App) error {
				if err := mergeDeliveredTo(tx, existing, obj.EnvelopeTo); err != nil {
					return err
				}
				return saveKey(tx, seen, key, "duplicate", existing.Id, msg.MessageID)
			})
		}
	}

	// Write the raw file before the rows: a crash in between leaves an orphan file
	// that the retry overwrites (same path), never a row without its file.
	if err := in.writeRaw(key, raw); err != nil {
		return "", "", err
	}

	var storedID string
	err = in.App.RunInTransaction(func(tx core.App) error {
		messages, err := tx.FindCollectionByNameOrId("messages")
		if err != nil {
			return err
		}
		rec := core.NewRecord(messages)
		applyParsed(rec, msg)
		mailbox := strings.ToLower(obj.EnvelopeTo)
		rec.Set("mailbox", mailbox)
		rec.Set("mailbox_domain", domainOf(mailbox))
		rec.Set("delivered_to", deliveredTo(nil, obj.EnvelopeTo))
		rec.Set("envelope_from", obj.EnvelopeFrom)
		if t, ok := spool.KeyTime(key); ok {
			rec.Set("received_at", t)
		} else {
			rec.Set("received_at", time.Now().UTC())
		}
		rec.Set("direction", "inbound")
		rec.Set("raw_path", key)
		rec.Set("raw_size", len(raw))
		rec.Set("r2_key", key)
		if err := tx.Save(rec); err != nil {
			return fmt.Errorf("save message: %w", err)
		}
		if err := replaceAttachments(tx, rec.Id, msg.Attachments); err != nil {
			return err
		}
		// Own domains first: thread participants exclude them.
		if err := touchDomain(tx, domainOf(mailbox)); err != nil {
			return err
		}
		if err := touchContact(tx, msg.From, rec.GetDateTime("received_at")); err != nil {
			return err
		}
		if err := threading.Assign(tx, rec); err != nil {
			return fmt.Errorf("thread: %w", err)
		}
		if err := reviveThread(tx, rec.GetString("thread")); err != nil {
			return err
		}
		storedID = rec.Id
		return saveKey(tx, seen, key, "stored", rec.Id, msg.MessageID)
	})
	if err != nil {
		return "", "", err
	}
	return Stored, storedID, nil
}

// RawPath is where the raw message for key lives under the mail dir.
func (in *Ingester) RawPath(key string) string {
	return filepath.Join(in.MailDir, filepath.FromSlash(key))
}

func (in *Ingester) writeRaw(key string, raw []byte) error {
	dst := in.RawPath(key)
	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".ingest-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op after a successful rename
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o640); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), dst)
}

func (in *Ingester) findKey(key string) (*core.Record, error) {
	rec, err := in.App.FindFirstRecordByData("ingested_keys", "key", key)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return rec, err
}

func (in *Ingester) recordFailure(key string, seen *core.Record, cause error) error {
	rec := seen
	if rec == nil {
		collection, err := in.App.FindCollectionByNameOrId("ingested_keys")
		if err != nil {
			return err
		}
		rec = core.NewRecord(collection)
		rec.Set("key", key)
	}
	rec.Set("status", "failed")
	msg := cause.Error()
	if len(msg) > 2000 {
		msg = msg[:2000]
	}
	rec.Set("error", msg)
	attempts := rec.GetInt("attempts") + 1
	if errors.Is(cause, ErrTooLarge) {
		attempts = max(attempts, MaxAttempts)
	}
	rec.Set("attempts", attempts)
	return in.App.Save(rec)
}

func saveKey(tx core.App, seen *core.Record, key, status, messageID, headerID string) error {
	rec := seen
	if rec == nil {
		collection, err := tx.FindCollectionByNameOrId("ingested_keys")
		if err != nil {
			return err
		}
		rec = core.NewRecord(collection)
		rec.Set("key", key)
	}
	rec.Set("status", status)
	rec.Set("message", messageID)
	rec.Set("message_id", headerID)
	rec.Set("error", "")
	return tx.Save(rec)
}

func mergeDeliveredTo(tx core.App, rec *core.Record, envelopeTo string) error {
	var current []string
	_ = rec.UnmarshalJSONField("delivered_to", &current)
	merged := deliveredTo(current, envelopeTo)
	if len(merged) == len(current) {
		return nil
	}
	rec.Set("delivered_to", merged)
	return tx.Save(rec)
}

func deliveredTo(current []string, envelopeTo string) []string {
	out := slices.Clone(current)
	if out == nil {
		out = []string{}
	}
	if envelopeTo != "" && !slices.Contains(out, envelopeTo) {
		out = append(out, envelopeTo)
	}
	return out
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

func (in *Ingester) logger() *slog.Logger {
	if in.Logger != nil {
		return in.Logger
	}
	return slog.Default()
}
