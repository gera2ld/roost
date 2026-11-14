// Package monitor watches the inbound pipeline and reports problems that would
// otherwise go unnoticed: the sweep unable to reach R2, spooled mail that never
// made it into the database, and a mailbox that has gone quiet for too long.
//
// Checks run on a schedule. Each problem is alerted once when it appears,
// repeated daily while it lasts, and followed by an all-clear when it goes
// away. That state is in memory, so a restart may repeat an alert once.
package monitor

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"ganigeorgiev.com/pocketbase/pocketbase/core"

	"roost/server/internal/ingest"
	"roost/server/internal/notify"
	"roost/server/internal/spool"
)

// Defaults for Monitor.
const (
	DefaultStaleAfter = 2 * time.Hour // spooled mail this old must be in the DB
	DefaultSweepGrace = 30 * time.Minute
	DefaultRepeat     = 24 * time.Hour
	lookback          = 48 * time.Hour // how far back the spool is reconciled
	maxNamed          = 5
)

// Problem is one thing worth telling the owner about. Key identifies it across
// checks; a changed key (say, a new failed message) counts as a new problem.
type Problem struct {
	Key    string
	Title  string
	Detail string
}

// Monitor runs the checks. Ingester may be nil (ingest not configured): only
// the idle check runs then. Alert may be nil: problems are logged only.
type Monitor struct {
	App        core.App
	Ingester   *ingest.Ingester
	Alert      func(ctx context.Context, html string)
	Logger     *slog.Logger
	IdleAfter  time.Duration // 0 disables the idle check
	StaleAfter time.Duration
	SweepGrace time.Duration
	Repeat     time.Duration

	mu       sync.Mutex
	started  time.Time
	lastList time.Time // last sweep that listed the spool
	listErr  error
	open     map[string]openProblem
}

type openProblem struct {
	Problem
	alertedAt time.Time
}

// SweepDone records the outcome of a sweep run.
func (m *Monitor) SweepDone(err error, at time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var listErr *ingest.ListError
	if errors.As(err, &listErr) {
		m.listErr = listErr.Err
		return
	}
	m.lastList, m.listErr = at, nil
}

// Check returns the current problems. It may ingest spooled keys the sweep
// missed (a Worker clock running behind, say), which is how it heals them.
func (m *Monitor) Check(ctx context.Context, now time.Time) ([]Problem, error) {
	var problems []Problem
	if m.Ingester != nil {
		if p := m.checkSweep(now); p != nil {
			problems = append(problems, *p)
		}
		p, err := m.checkSpool(ctx, now)
		if err != nil {
			return problems, err
		}
		if p != nil {
			problems = append(problems, *p)
		}
	}
	p, err := m.checkIdle(now)
	if err != nil {
		return problems, err
	}
	if p != nil {
		problems = append(problems, *p)
	}
	return problems, nil
}

func (m *Monitor) checkSweep(now time.Time) *Problem {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.started.IsZero() {
		m.started = now
	}
	since := m.lastList
	if since.IsZero() {
		since = m.started
	}
	if now.Sub(since) < or(m.SweepGrace, DefaultSweepGrace) {
		return nil
	}
	detail := "No successful listing of the R2 spool since " + since.UTC().Format(time.RFC3339) + "."
	if m.listErr != nil {
		detail += "\n" + m.listErr.Error()
	}
	return &Problem{Key: "sweep", Title: "Mail sweep cannot reach R2", Detail: detail}
}

// checkSpool finds spooled mail older than StaleAfter that is not stored: keys
// the sweep never picked up (ingested now, if possible) and keys that failed.
func (m *Monitor) checkSpool(ctx context.Context, now time.Time) (*Problem, error) {
	stale := now.Add(-or(m.StaleAfter, DefaultStaleAfter))
	cut := spool.KeyPrefixAt(stale)

	failed := map[string]string{}
	recs, err := m.App.FindRecordsByFilter("ingested_keys", "status = 'failed' && key < {:cut}", "key", 0, 0,
		map[string]any{"cut": cut})
	if err != nil {
		return nil, err
	}
	for _, r := range recs {
		failed[r.GetString("key")] = r.GetString("error")
	}

	keys, err := m.Ingester.Spool.List(ctx, spool.KeyPrefixAt(now.Add(-lookback)))
	if err != nil {
		// The sweep check reports an unreachable spool; keep the DB-side findings.
		m.logger().Warn("monitor: list spool", "error", err)
		keys = nil
	}
	for _, key := range keys {
		if !spool.ValidKey(key) || key >= cut {
			continue
		}
		if _, err := m.App.FindFirstRecordByData("ingested_keys", "key", key); err == nil {
			continue // stored, duplicate, or already in failed
		}
		// Older than the sweep's window and never seen: ingest it now.
		if _, err := m.Ingester.Ingest(ctx, key); err != nil {
			failed[key] = err.Error()
			continue
		}
		m.logger().Warn("monitor: ingested a key the sweep missed", "key", key)
	}
	if len(failed) == 0 {
		return nil, nil
	}

	names := make([]string, 0, len(failed))
	for k := range failed {
		names = append(names, k)
	}
	slices.Sort(names)
	var b strings.Builder
	for i, k := range names {
		if i == maxNamed {
			fmt.Fprintf(&b, "…and %d more\n", len(names)-maxNamed)
			break
		}
		fmt.Fprintf(&b, "%s: %s\n", k, firstLine(failed[k]))
	}
	title := fmt.Sprintf("%d spooled message(s) not ingested", len(names))
	return &Problem{Key: "ingest:" + strings.Join(names, ","), Title: title,
		Detail: strings.TrimSpace(b.String()) + "\nThe raw mail is still in R2; fix the cause and the sweep retries, or run `roost ingest KEY`."}, nil
}

func (m *Monitor) checkIdle(now time.Time) (*Problem, error) {
	if m.IdleAfter <= 0 {
		return nil, nil
	}
	newest, ok, err := ingest.NewestKeyTime(m.App, now)
	if err != nil || !ok {
		return nil, err // nothing received yet: nothing to compare against
	}
	if now.Sub(newest) < m.IdleAfter {
		return nil, nil
	}
	days := int(now.Sub(newest).Hours() / 24)
	return &Problem{Key: "idle", Title: fmt.Sprintf("No new mail for %d days", days),
		Detail: "Last message spooled " + newest.UTC().Format(time.RFC3339) + ". Check Email Routing and the Worker."}, nil
}

// Run checks once and sends alerts for new, repeating and resolved problems.
func (m *Monitor) Run(ctx context.Context, now time.Time) {
	problems, err := m.Check(ctx, now)
	if err != nil {
		m.logger().Error("monitor check", "error", err)
		return // a failed check says nothing about which problems resolved
	}
	m.mu.Lock()
	if m.open == nil {
		m.open = map[string]openProblem{}
	}
	var send []string
	current := map[string]bool{}
	for _, p := range problems {
		current[p.Key] = true
		prev, seen := m.open[p.Key]
		if seen && now.Sub(prev.alertedAt) < or(m.Repeat, DefaultRepeat) {
			continue
		}
		m.open[p.Key] = openProblem{Problem: p, alertedAt: now}
		m.logger().Warn("monitor: "+p.Title, "detail", p.Detail)
		send = append(send, notify.FormatOps(p.Title, p.Detail, false))
	}
	for key, p := range m.open {
		if !current[key] {
			delete(m.open, key)
			m.logger().Info("monitor resolved: " + p.Title)
			send = append(send, notify.FormatOps(p.Title, p.Detail, true))
		}
	}
	m.mu.Unlock()

	if m.Alert != nil {
		for _, text := range send {
			m.Alert(ctx, text)
		}
	}
}

func (m *Monitor) logger() *slog.Logger {
	if m.Logger != nil {
		return m.Logger
	}
	return slog.Default()
}

func or(d, fallback time.Duration) time.Duration {
	if d > 0 {
		return d
	}
	return fallback
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(s, "\n")
	if r := []rune(s); len(r) > 160 {
		s = string(r[:160]) + "…"
	}
	return s
}
