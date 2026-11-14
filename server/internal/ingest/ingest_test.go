package ingest

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tests"

	"roost/server/internal/spool"
	"roost/server/internal/store"
)

const (
	key1 = "2026/11/13/093000-00000000-0000-4000-8000-000000000001.eml"
	key2 = "2026/11/13/093001-00000000-0000-4000-8000-000000000002.eml"
	key3 = "2026/11/13/100000-00000000-0000-4000-8000-000000000003.eml"
)

func setup(t *testing.T) (*Ingester, *spool.Memory, core.App) {
	t.Helper()
	app, err := tests.NewTestApp()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.Cleanup)
	if err := store.Sync(app); err != nil {
		t.Fatal(err)
	}
	mem := spool.NewMemory()
	return &Ingester{App: app, Spool: mem, MailDir: t.TempDir()}, mem, app
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "mailparse", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func count(t *testing.T, app core.App, collection, filter string) int {
	t.Helper()
	n, err := app.CountRecords(collection)
	if filter != "" {
		recs, ferr := app.FindRecordsByFilter(collection, filter, "", 0, 0)
		n, err = int64(len(recs)), ferr
	}
	if err != nil {
		t.Fatal(err)
	}
	return int(n)
}

func TestIngestStoresMessageFileAndAttachments(t *testing.T) {
	in, mem, app := setup(t)
	raw := fixture(t, "multipart-attachments.eml")
	mem.Put(key1, raw, "sender@example.com", "me@roost.test")

	out, err := in.Ingest(context.Background(), key1)
	if err != nil || out != Stored {
		t.Fatalf("ingest = %v, %v", out, err)
	}

	msg, err := app.FindFirstRecordByData("messages", "message_id", "multi-1@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if msg.GetString("raw_path") != key1 || msg.GetInt("raw_size") != len(raw) {
		t.Errorf("raw path/size = %q/%d", msg.GetString("raw_path"), msg.GetInt("raw_size"))
	}
	if msg.GetString("envelope_from") != "sender@example.com" {
		t.Errorf("envelope_from = %q", msg.GetString("envelope_from"))
	}
	var delivered []string
	_ = msg.UnmarshalJSONField("delivered_to", &delivered)
	if len(delivered) != 1 || delivered[0] != "me@roost.test" {
		t.Errorf("delivered_to = %v", delivered)
	}
	if got := msg.GetDateTime("received_at").Time(); !got.Equal(time.Date(2026, 11, 13, 9, 30, 0, 0, time.UTC)) {
		t.Errorf("received_at = %v", got)
	}
	if n := count(t, app, "attachments", ""); n != 3 {
		t.Errorf("attachments = %d, want 3", n)
	}

	onDisk, err := os.ReadFile(in.RawPath(key1))
	if err != nil || string(onDisk) != string(raw) {
		t.Fatalf("raw file mismatch: %v", err)
	}

	key, err := app.FindFirstRecordByData("ingested_keys", "key", key1)
	if err != nil || key.GetString("status") != "stored" || key.GetString("message") != msg.Id {
		t.Fatalf("ingested key = %v, %v", key, err)
	}
}

func TestIngestIsIdempotent(t *testing.T) {
	in, mem, app := setup(t)
	mem.Put(key1, fixture(t, "plain-utf8.eml"), "a@example.com", "me@roost.test")

	for i := 0; i < 3; i++ {
		if _, err := in.Ingest(context.Background(), key1); err != nil {
			t.Fatal(err)
		}
	}
	out, _ := in.Ingest(context.Background(), key1)
	if out != Skipped {
		t.Errorf("repeat ingest = %q, want skipped", out)
	}
	if n := count(t, app, "messages", ""); n != 1 {
		t.Errorf("messages = %d, want 1", n)
	}
}

func TestSameMessageIDToTwoAddressesIsMerged(t *testing.T) {
	in, mem, app := setup(t)
	raw := fixture(t, "plain-utf8.eml")
	mem.Put(key1, raw, "a@example.com", "me@roost.test")
	mem.Put(key2, raw, "a@example.com", "other@roost.test")

	if out, err := in.Ingest(context.Background(), key1); err != nil || out != Stored {
		t.Fatalf("first = %v, %v", out, err)
	}
	if out, err := in.Ingest(context.Background(), key2); err != nil || out != Duplicate {
		t.Fatalf("second = %v, %v", out, err)
	}
	if n := count(t, app, "messages", ""); n != 1 {
		t.Fatalf("messages = %d, want 1", n)
	}
	msg, _ := app.FindFirstRecordByData("messages", "message_id", "plain-1@example.com")
	var delivered []string
	_ = msg.UnmarshalJSONField("delivered_to", &delivered)
	if len(delivered) != 2 {
		t.Errorf("delivered_to = %v, want both addresses", delivered)
	}
	if _, err := os.Stat(in.RawPath(key2)); !os.IsNotExist(err) {
		t.Errorf("duplicate should not write a second raw file")
	}
	if n := count(t, app, "ingested_keys", "status = 'duplicate'"); n != 1 {
		t.Errorf("duplicate keys = %d", n)
	}
}

func TestMissingMessageIDIsNotDeduped(t *testing.T) {
	in, mem, app := setup(t)
	raw := fixture(t, "no-message-id.eml")
	mem.Put(key1, raw, "", "me@roost.test")
	mem.Put(key2, raw, "", "me@roost.test")
	for _, k := range []string{key1, key2} {
		if out, err := in.Ingest(context.Background(), k); err != nil || out != Stored {
			t.Fatalf("%s = %v, %v", k, out, err)
		}
	}
	if n := count(t, app, "messages", ""); n != 2 {
		t.Errorf("messages = %d, want 2", n)
	}
}

func TestFailureIsRecordedThenRetried(t *testing.T) {
	in, mem, app := setup(t)
	// Not in the spool yet: the fetch fails.
	if _, err := in.Ingest(context.Background(), key1); err == nil {
		t.Fatal("expected fetch failure")
	}
	rec, err := app.FindFirstRecordByData("ingested_keys", "key", key1)
	if err != nil || rec.GetString("status") != "failed" || rec.GetInt("attempts") != 1 {
		t.Fatalf("failure not recorded: %v %v", rec, err)
	}
	if n := count(t, app, "messages", ""); n != 0 {
		t.Fatalf("messages = %d after failure", n)
	}

	mem.Put(key1, fixture(t, "plain-utf8.eml"), "", "me@roost.test")
	if out, err := in.Ingest(context.Background(), key1); err != nil || out != Stored {
		t.Fatalf("retry = %v, %v", out, err)
	}
	rec, _ = app.FindFirstRecordByData("ingested_keys", "key", key1)
	if rec.GetString("status") != "stored" || rec.GetString("error") != "" {
		t.Errorf("key after retry = %s %q", rec.GetString("status"), rec.GetString("error"))
	}
}

func TestInvalidKeyRejected(t *testing.T) {
	in, _, _ := setup(t)
	for _, k := range []string{"../../etc/passwd", "2026/11/13/093000-x.eml", ""} {
		if _, err := in.Ingest(context.Background(), k); err == nil {
			t.Errorf("key %q accepted", k)
		}
	}
}

func TestSweepCatchesUpAndRetries(t *testing.T) {
	in, mem, app := setup(t)
	mem.Put(key1, fixture(t, "plain-utf8.eml"), "", "me@roost.test")
	mem.Put(key2, fixture(t, "gbk.eml"), "", "me@roost.test")
	mem.Put("not-a-spool-key.txt", []byte("junk"), "", "")

	res, err := in.Sweep(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Stored != 2 {
		t.Fatalf("first sweep = %+v", res)
	}

	// A key arriving later is picked up; already-ingested keys are skipped.
	mem.Put(key3, fixture(t, "reply.eml"), "", "me@roost.test")
	res, err = in.Sweep(context.Background())
	if err != nil || res.Stored != 1 {
		t.Fatalf("second sweep = %+v, %v", res, err)
	}
	if n := count(t, app, "messages", ""); n != 3 {
		t.Errorf("messages = %d, want 3", n)
	}
}

func TestSweepStartsAMarginBeforeNewestKey(t *testing.T) {
	in, mem, _ := setup(t)
	mem.Put(key3, fixture(t, "plain-utf8.eml"), "", "me@roost.test")
	if _, err := in.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	start, err := in.sweepStart()
	if err != nil {
		t.Fatal(err)
	}
	if want := "2026/11/13/090000"; start != want {
		t.Errorf("sweep start = %q, want %q", start, want)
	}
}

func TestOnStoredFiresOnlyForNewMessages(t *testing.T) {
	in, mem, app := setup(t)
	var stored []string
	in.OnStored = func(id string) { stored = append(stored, id) }
	raw := fixture(t, "plain-utf8.eml")
	mem.Put(key1, raw, "a@example.com", "me@roost.test")
	mem.Put(key2, raw, "a@example.com", "other@roost.test")
	for _, key := range []string{key1, key1, key2} { // stored, skipped, duplicate
		if _, err := in.Ingest(context.Background(), key); err != nil {
			t.Fatal(err)
		}
	}
	msg, _ := app.FindFirstRecordByData("messages", "message_id", "plain-1@example.com")
	if len(stored) != 1 || stored[0] != msg.Id {
		t.Errorf("OnStored calls = %v, want [%s]", stored, msg.Id)
	}
}

func TestOversizeMessageIsNotRetried(t *testing.T) {
	in, mem, app := setup(t)
	in.MaxSize = 10
	mem.Put(key1, fixture(t, "plain-utf8.eml"), "", "me@roost.test")
	if _, err := in.Ingest(context.Background(), key1); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err = %v", err)
	}
	rec, _ := app.FindFirstRecordByData("ingested_keys", "key", key1)
	if rec.GetInt("attempts") != MaxAttempts {
		t.Errorf("attempts = %d, want %d so the sweep stops retrying", rec.GetInt("attempts"), MaxAttempts)
	}
}

func TestSweepIgnoresFutureDatedKeys(t *testing.T) {
	in, mem, _ := setup(t)
	mem.Put(key3, fixture(t, "plain-utf8.eml"), "", "me@roost.test")
	future := spool.KeyPrefixAt(time.Now().AddDate(5, 0, 0)) + "-00000000-0000-4000-8000-000000000009.eml"
	mem.Put(future, fixture(t, "gbk.eml"), "", "me@roost.test")
	if _, err := in.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	start, err := in.sweepStart()
	if err != nil {
		t.Fatal(err)
	}
	if want := "2026/11/13/090000"; start != want {
		t.Errorf("sweep start = %q, want %q (future key ignored)", start, want)
	}
}

func TestTrimKeysKeepsRecentAndNewest(t *testing.T) {
	in, mem, app := setup(t)
	old1 := "2026/01/01/000000-00000000-0000-4000-8000-00000000000a.eml"
	old2 := "2026/01/02/000000-00000000-0000-4000-8000-00000000000b.eml"
	mem.Put(old1, fixture(t, "plain-utf8.eml"), "", "me@roost.test")
	mem.Put(old2, fixture(t, "gbk.eml"), "", "me@roost.test")
	mem.Put(key1, fixture(t, "reply.eml"), "", "me@roost.test")
	for _, k := range []string{old1, old2, key1} {
		if _, err := in.Ingest(context.Background(), k); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)
	before := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

	// Rows created recently (an import of old mail) are kept.
	if n, err := TrimKeys(app, before, now); err != nil || n != 0 {
		t.Fatalf("trim of fresh rows = %d, %v", n, err)
	}
	if _, err := app.DB().NewQuery("UPDATE {{ingested_keys}} SET [[created]] = '2026-01-03 00:00:00.000Z'").Execute(); err != nil {
		t.Fatal(err)
	}
	if n, err := TrimKeys(app, before, now); err != nil || n != 2 {
		t.Fatalf("trimmed = %d, %v; want the two old keys", n, err)
	}
	// Everything old: the newest key still stays to anchor the sweep.
	if n, err := TrimKeys(app, now, now); err != nil || n != 0 {
		t.Errorf("newest key trimmed: %d, %v", n, err)
	}
	if n := count(t, app, "ingested_keys", ""); n != 1 {
		t.Errorf("keys left = %d", n)
	}
}
