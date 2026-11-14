package monitor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tests"

	"roost/server/internal/ingest"
	"roost/server/internal/spool"
	"roost/server/internal/store"
)

func setup(t *testing.T) (*Monitor, *spool.Memory, core.App) {
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
	in := &ingest.Ingester{App: app, Spool: mem, MailDir: t.TempDir()}
	return &Monitor{App: app, Ingester: in}, mem, app
}

func keyAt(t time.Time, n int) string {
	return spool.KeyPrefixAt(t) + "-00000000-0000-4000-8000-00000000000" + string(rune('0'+n)) + ".eml"
}

func fixture(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "mailparse", "testdata", "plain-utf8.eml"))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func keys(ps []Problem) []string {
	var out []string
	for _, p := range ps {
		out = append(out, p.Key)
	}
	return out
}

func TestSweepProblemAfterGrace(t *testing.T) {
	m, _, _ := setup(t)
	now := time.Now()
	m.SweepDone(nil, now)
	if ps, _ := m.Check(context.Background(), now.Add(10*time.Minute)); len(ps) != 0 {
		t.Fatalf("problems right after a good sweep: %v", keys(ps))
	}
	m.SweepDone(&ingest.ListError{Err: errors.New("403 forbidden")}, now.Add(20*time.Minute))
	ps, _ := m.Check(context.Background(), now.Add(time.Hour))
	if len(ps) != 1 || ps[0].Key != "sweep" || !strings.Contains(ps[0].Detail, "403 forbidden") {
		t.Fatalf("problems = %+v", ps)
	}
	// Per-key ingest errors are not a listing failure.
	m.SweepDone(errors.Join(errors.New("one key failed")), now.Add(time.Hour))
	if ps, _ := m.Check(context.Background(), now.Add(time.Hour)); len(ps) != 0 {
		t.Errorf("problems = %v", keys(ps))
	}
}

func TestSpoolCheckHealsMissedKeysAndReportsFailures(t *testing.T) {
	m, mem, app := setup(t)
	now := time.Now()
	m.SweepDone(nil, now)
	missed := keyAt(now.Add(-5*time.Hour), 1)
	mem.Put(missed, fixture(t), "", "me@roost.test")
	fresh := keyAt(now.Add(-10*time.Minute), 2) // the sweep's job, not stale yet
	mem.Put(fresh, []byte("x"), "", "me@roost.test")

	ps, err := m.Check(context.Background(), now)
	if err != nil || len(ps) != 0 {
		t.Fatalf("problems = %v, %v", keys(ps), err)
	}
	if rec, err := app.FindFirstRecordByData("ingested_keys", "key", missed); err != nil || rec.GetString("status") != "stored" {
		t.Fatalf("missed key not ingested: %v", err)
	}

	broken := keyAt(now.Add(-3*time.Hour), 3)
	m.Ingester.MaxSize = 10
	mem.Put(broken, fixture(t), "", "me@roost.test")
	ps, _ = m.Check(context.Background(), now)
	if len(ps) != 1 || ps[0].Key != "ingest:"+broken || !strings.Contains(ps[0].Detail, "too large") {
		t.Fatalf("problems = %+v", ps)
	}
}

func TestIdleCheck(t *testing.T) {
	m, mem, _ := setup(t)
	m.Ingester = nil // ingest off: only the idle check runs
	m.IdleAfter = 3 * 24 * time.Hour
	now := time.Now()
	if ps, _ := m.Check(context.Background(), now); len(ps) != 0 {
		t.Fatal("idle reported with no mail at all")
	}
	in := &ingest.Ingester{App: m.App, Spool: mem, MailDir: t.TempDir()}
	k := keyAt(now.Add(-4*24*time.Hour), 1)
	mem.Put(k, fixture(t), "", "me@roost.test")
	if _, err := in.Ingest(context.Background(), k); err != nil {
		t.Fatal(err)
	}
	ps, _ := m.Check(context.Background(), now)
	if len(ps) != 1 || ps[0].Title != "No new mail for 4 days" {
		t.Fatalf("problems = %+v", ps)
	}
}

func TestRunAlertsOnceRepeatsAndResolves(t *testing.T) {
	m, _, _ := setup(t)
	var sent []string
	m.Alert = func(_ context.Context, html string) { sent = append(sent, html) }
	now := time.Now()
	m.SweepDone(&ingest.ListError{Err: errors.New("<down>")}, now)

	m.Run(context.Background(), now.Add(time.Hour))
	m.Run(context.Background(), now.Add(2*time.Hour))
	if len(sent) != 1 || !strings.Contains(sent[0], "&lt;down&gt;") {
		t.Fatalf("sent = %q", sent)
	}
	m.Run(context.Background(), now.Add(26*time.Hour))
	if len(sent) != 2 {
		t.Fatalf("not repeated after a day: %q", sent)
	}
	m.SweepDone(nil, now.Add(27*time.Hour))
	m.Run(context.Background(), now.Add(27*time.Hour))
	if len(sent) != 3 || !strings.Contains(sent[2], "Resolved") {
		t.Errorf("no all-clear: %q", sent)
	}
}
