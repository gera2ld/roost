package ingest

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"ganigeorgiev.com/pocketbase/pocketbase/core"

	"roost/server/internal/mailparse"
	"roost/server/internal/spool"
)

// ImportResult counts the outcome of ImportFiles.
type ImportResult struct {
	Stored, Dupes, Skipped, Failed int
}

// ImportFiles ingests local .eml files (or directories of them) through the same
// path as spooled mail. Each file gets a key from its Date header (else its
// mtime) and a UUID derived from its content, so importing twice is a no-op.
// envelopeTo overrides the mailbox; by default it is the first To address.
func ImportFiles(ctx context.Context, app core.App, mailDir string, paths []string, envelopeTo string) (ImportResult, error) {
	mem := spool.NewMemory()
	var keys []string
	var errs []error
	var res ImportResult

	add := func(path string, info fs.FileInfo) {
		raw, err := os.ReadFile(path)
		if err != nil {
			errs = append(errs, err)
			res.Failed++
			return
		}
		key, from, to := importKey(raw, info, envelopeTo)
		mem.Put(key, raw, from, to)
		keys = append(keys, key)
	}
	for _, p := range paths {
		err := filepath.WalkDir(p, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || (path != p && !strings.EqualFold(filepath.Ext(path), ".eml")) {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			add(path, info)
			return nil
		})
		if err != nil {
			errs = append(errs, err)
		}
	}

	in := &Ingester{App: app, Spool: mem, MailDir: mailDir, Logger: app.Logger()}
	for _, key := range keys {
		outcome, err := in.Ingest(ctx, key)
		switch {
		case err != nil:
			errs = append(errs, fmt.Errorf("%s: %w", key, err))
			res.Failed++
		case outcome == Stored:
			res.Stored++
		case outcome == Duplicate:
			res.Dupes++
		default:
			res.Skipped++
		}
	}
	return res, errors.Join(errs...)
}

func importKey(raw []byte, info fs.FileInfo, envelopeTo string) (key, from, to string) {
	at := info.ModTime()
	if msg, err := mailparse.Parse(raw); err == nil {
		if !msg.Date.IsZero() {
			at = msg.Date
		}
		from = msg.From.Address
		if len(msg.To) > 0 {
			to = msg.To[0].Address
		}
	}
	if envelopeTo != "" {
		to = envelopeTo
	}
	h := sha256.Sum256(raw)
	x := fmt.Sprintf("%x", h[:16])
	// Shaped like a v4 UUID so it passes spool.ValidKey.
	id := x[0:8] + "-" + x[8:12] + "-4" + x[13:16] + "-8" + x[17:20] + "-" + x[20:32]
	return spool.KeyPrefixAt(at) + "-" + id + ".eml", from, to
}
