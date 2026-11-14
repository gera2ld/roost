package ingest

import (
	"errors"
	"os"
	"path/filepath"
	"time"

	"ganigeorgiev.com/pocketbase/pocketbase/core"

	"roost/server/internal/spool"
)

// TrashRetention is how long a trashed thread is kept before it is purged.
const TrashRetention = 30 * 24 * time.Hour

// PurgeThread permanently deletes a thread, its messages, their attachment rows
// and raw files. Rows go first, files after commit: a crash in between leaves an
// orphan file, never a row pointing at a missing file. Ingested-key rows stay so
// the sweep never re-ingests a purged message while its R2 object still exists.
func PurgeThread(app core.App, mailDir, threadID string) error {
	var files []string
	err := app.RunInTransaction(func(tx core.App) error {
		messages, err := tx.FindRecordsByFilter("messages", "thread = {:t}", "", 0, 0, map[string]any{"t": threadID})
		if err != nil {
			return err
		}
		for _, m := range messages {
			if key := m.GetString("raw_path"); spool.ValidKey(key) {
				// Another message (a duplicate delivery) never shares a raw file,
				// but check anyway before deleting bytes.
				others, err := tx.FindRecordsByFilter("messages", "raw_path = {:k} && id != {:id}", "", 1, 0,
					map[string]any{"k": key, "id": m.Id})
				if err != nil {
					return err
				}
				if len(others) == 0 {
					files = append(files, filepath.Join(mailDir, filepath.FromSlash(key)))
				}
			}
			if err := tx.Delete(m); err != nil {
				return err
			}
		}
		thread, err := tx.FindRecordById("threads", threadID)
		if err == nil {
			return tx.Delete(thread)
		}
		return nil // already removed by the message delete hooks
	})
	if err != nil {
		return err
	}
	var errs []error
	for _, f := range files {
		if err := os.Remove(f); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// PurgeExpiredTrash purges threads trashed longer than TrashRetention ago.
func PurgeExpiredTrash(app core.App, mailDir string, now time.Time) (int, error) {
	cutoff := now.Add(-TrashRetention).UTC().Format("2006-01-02 15:04:05.000Z")
	threads, err := app.FindRecordsByFilter("threads", "trashed_at != '' && trashed_at < {:cutoff}", "", 0, 0,
		map[string]any{"cutoff": cutoff})
	if err != nil {
		return 0, err
	}
	var errs []error
	purged := 0
	for _, t := range threads {
		if err := PurgeThread(app, mailDir, t.Id); err != nil {
			errs = append(errs, err)
			continue
		}
		purged++
	}
	return purged, errors.Join(errs...)
}
