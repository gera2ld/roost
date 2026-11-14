// Package spool reads the inbound R2 spool the Email Worker writes.
//
// Keys look like YYYY/MM/DD/HHMMSS-<uuid>.eml (UTC), so lexical order is arrival
// order. Each object carries the SMTP envelope as custom metadata, because with
// catch-all routing the real recipient is not reliably in the headers.
package spool

import (
	"context"
	"io"
	"regexp"
	"strings"
	"time"
)

// Metadata keys the Worker sets on every object (keep in sync with worker/src).
const (
	MetaEnvelopeFrom = "envelope-from"
	MetaEnvelopeTo   = "envelope-to"
)

// Object describes one spooled message.
type Object struct {
	Key          string
	Size         int64
	EnvelopeFrom string
	EnvelopeTo   string
}

// Spool is the subset of R2 that ingest needs.
type Spool interface {
	// List returns every key strictly after startAfter, in lexical order.
	List(ctx context.Context, startAfter string) ([]string, error)
	// Get opens one object. The caller closes the reader.
	Get(ctx context.Context, key string) (io.ReadCloser, Object, error)
}

const keyTimeLayout = "2006/01/02/150405"

var keyPattern = regexp.MustCompile(`^\d{4}/\d{2}/\d{2}/\d{6}-[0-9a-f-]{36}\.eml$`)

// ValidKey reports whether key has the exact shape the Worker writes. Anything else
// is rejected before it can reach the filesystem.
func ValidKey(key string) bool { return keyPattern.MatchString(key) }

// KeyTime returns the arrival time encoded in a key.
func KeyTime(key string) (time.Time, bool) {
	if len(key) < len(keyTimeLayout) {
		return time.Time{}, false
	}
	t, err := time.Parse(keyTimeLayout, key[:len(keyTimeLayout)])
	return t, err == nil
}

// KeyPrefixAt renders t in key form, usable as a List startAfter bound.
func KeyPrefixAt(t time.Time) string { return t.UTC().Format(keyTimeLayout) }

// metaLookup finds a metadata value regardless of how the S3 layer cased the key.
func metaLookup(meta map[string]string, key string) string {
	for k, v := range meta {
		if strings.EqualFold(k, key) || strings.EqualFold(k, "x-amz-meta-"+key) {
			return v
		}
	}
	return ""
}
