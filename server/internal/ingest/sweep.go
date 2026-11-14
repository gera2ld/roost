package ingest

import (
	"context"
	"errors"
	"time"

	"ganigeorgiev.com/pocketbase/dbx"
	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/types"

	"roost/server/internal/spool"
)

// SweepMargin is how far before the newest ingested key the sweep starts listing.
// Keys are written by concurrent Workers with second resolution, so a key can land
// slightly "behind" one already ingested.
const SweepMargin = time.Hour

// ListError means the sweep could not list the spool at all (R2 unreachable or
// credentials wrong), as opposed to individual keys failing to ingest.
type ListError struct{ Err error }

func (e *ListError) Error() string { return "list spool: " + e.Err.Error() }
func (e *ListError) Unwrap() error { return e.Err }

// SweepResult counts what one sweep did.
type SweepResult struct {
	Listed  int
	Stored  int
	Dupes   int
	Retried int
	Failed  int
}

// Sweep ingests every spooled key the database has not seen, starting a margin
// before the newest ingested key, then retries keys that failed earlier.
func (in *Ingester) Sweep(ctx context.Context) (SweepResult, error) {
	var res SweepResult

	startAfter, err := in.sweepStart()
	if err != nil {
		return res, err
	}
	keys, err := in.Spool.List(ctx, startAfter)
	if err != nil {
		return res, &ListError{err}
	}
	res.Listed = len(keys)

	var errs []error
	for _, key := range keys {
		if ctx.Err() != nil {
			return res, ctx.Err()
		}
		if !spool.ValidKey(key) {
			continue
		}
		outcome, err := in.Ingest(ctx, key)
		res.count(outcome, err)
		if err != nil {
			errs = append(errs, err)
		}
	}

	failed, err := in.App.FindRecordsByFilter("ingested_keys",
		"status = 'failed' && attempts < {:max}", "key", 100, 0, map[string]any{"max": MaxAttempts})
	if err != nil {
		return res, errors.Join(append(errs, err)...)
	}
	for _, rec := range failed {
		key := rec.GetString("key")
		if keyListedAfter(key, startAfter) {
			continue // already attempted above
		}
		res.Retried++
		outcome, err := in.Ingest(ctx, key)
		res.count(outcome, err)
		if err != nil {
			errs = append(errs, err)
		}
	}
	return res, errors.Join(errs...)
}

func (r *SweepResult) count(outcome Outcome, err error) {
	switch {
	case err != nil:
		r.Failed++
	case outcome == Stored:
		r.Stored++
	case outcome == Duplicate:
		r.Dupes++
	}
}

func (in *Ingester) sweepStart() (string, error) {
	t, ok, err := NewestKeyTime(in.App, time.Now())
	if err != nil || !ok {
		return "", err // nothing ingested yet: list everything
	}
	return spool.KeyPrefixAt(t.Add(-SweepMargin)), nil
}

// NewestKeyTime is the time of the newest ingested key not after now. Keys
// dated in the future are ignored: an imported message with a bogus Date header
// must not push the sweep's starting point past real mail.
func NewestKeyTime(app core.App, now time.Time) (time.Time, bool, error) {
	newest, err := newestKey(app, now)
	if err != nil {
		return time.Time{}, false, err
	}
	t, ok := spool.KeyTime(newest)
	return t, ok, nil
}

func newestKey(app core.App, now time.Time) (string, error) {
	var newest string
	err := app.DB().NewQuery("SELECT COALESCE(MAX([[key]]), '') FROM {{ingested_keys}} WHERE [[key]] < {:limit}").
		Bind(dbx.Params{"limit": spool.KeyPrefixAt(now.Add(time.Minute))}).Row(&newest)
	return newest, err
}

// TrimKeys deletes ingested_keys rows for keys older than before, once the
// spool objects they guard have expired. Rows created after before are kept
// (an import of old mail stays deduped on its key for the same window), and so
// is the newest key, which anchors the sweep.
func TrimKeys(app core.App, before, now time.Time) (int64, error) {
	newest, err := newestKey(app, now)
	if err != nil || newest == "" {
		return 0, err
	}
	res, err := app.DB().NewQuery("DELETE FROM {{ingested_keys}} WHERE [[key]] < {:cut} AND [[key]] < {:newest} AND [[created]] < {:created}").
		Bind(dbx.Params{
			"cut":     spool.KeyPrefixAt(before),
			"newest":  newest,
			"created": before.UTC().Format(types.DefaultDateLayout),
		}).Execute()
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func keyListedAfter(key, startAfter string) bool { return key > startAfter }
