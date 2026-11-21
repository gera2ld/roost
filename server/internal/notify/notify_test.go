package notify

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tests"

	"roost/server/internal/store"
)

func TestFormatOneEscapesUntrustedText(t *testing.T) {
	text, button := FormatOne(Item{
		FromName: "Eve <b>", FromAddress: "eve@x.test", Mailbox: "me@roost.test",
		Subject: `<a href="javascript:x">win</a> & more`, Body: "Hi <i>there</i>", ThreadID: "t1",
	}, "https://mail.example.com/")
	for _, bad := range []string{"<a href", "<i>", "Eve <b>"} {
		if strings.Contains(text, bad) {
			t.Errorf("unescaped %q in %s", bad, text)
		}
	}
	if !strings.Contains(text, "&lt;a href=\"javascript:x\"&gt;win&lt;/a&gt; &amp; more") || !strings.Contains(text, "to me@roost.test") {
		t.Errorf("text = %s", text)
	}
	if button == nil || button.URL != "https://mail.example.com/mail/t1" {
		t.Errorf("button = %+v", button)
	}
	if _, b := FormatOne(Item{ThreadID: "t1"}, ""); b != nil {
		t.Error("button without a public URL")
	}
	if _, b := FormatOne(Item{ThreadID: "t1"}, "javascript:alert(1)"); b != nil {
		t.Error("button with a non-http public URL")
	}
}

func TestPreview(t *testing.T) {
	if got := Preview("Thanks!\n\nOn Mon, X wrote:\n> old\n>> older\n"); got != "Thanks! On Mon, X wrote:" {
		t.Errorf("preview = %q", got)
	}
	long := strings.Repeat("你好", 150)
	got := Preview(long)
	if !strings.HasSuffix(got, "…") || len([]rune(got)) != PreviewRunes+1 {
		t.Errorf("long preview has %d runes", len([]rune(got)))
	}
}

func TestFormatBatch(t *testing.T) {
	items := make([]Item, 12)
	for i := range items {
		items[i] = Item{FromName: "S", Subject: "x"}
	}
	text, button := FormatBatch(items, "https://m.test")
	if !strings.HasPrefix(text, "<b>12 new messages</b>") || !strings.Contains(text, "…and 2 more") ||
		strings.Count(text, "•") != 10 || button.URL != "https://m.test/inbox" {
		t.Errorf("batch = %s / %+v", text, button)
	}
}

func TestTelegramRequest(t *testing.T) {
	var path string
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &got)
		_, _ = w.Write([]byte(`{"ok":true,"result":{}}`))
	}))
	defer srv.Close()
	bot := &Telegram{Token: "123:SECRET", ChatID: "42", BaseURL: srv.URL}
	if err := bot.Send(context.Background(), "<b>hi</b>", &Button{Text: "Open", URL: "https://m.test/inbox/t"}); err != nil {
		t.Fatal(err)
	}
	if path != "/bot123:SECRET/sendMessage" || got["chat_id"] != "42" || got["parse_mode"] != "HTML" {
		t.Errorf("path %s body %v", path, got)
	}
	kb := got["reply_markup"].(map[string]any)["inline_keyboard"].([]any)[0].([]any)[0].(map[string]any)
	if kb["url"] != "https://m.test/inbox/t" {
		t.Errorf("keyboard = %v", kb)
	}
}

func TestTelegramErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(429)
		_, _ = w.Write([]byte(`{"ok":false,"error_code":429,"description":"Too Many Requests","parameters":{"retry_after":7}}`))
	}))
	bot := &Telegram{Token: "123:SECRET", ChatID: "42", BaseURL: srv.URL}
	var apiErr *APIError
	if err := bot.Send(context.Background(), "x", nil); !errors.As(err, &apiErr) || apiErr.RetryAfter != 7*time.Second {
		t.Fatalf("err = %#v", err)
	}
	srv.Close() // now a network error
	err := bot.Send(context.Background(), "x", nil)
	if err == nil || strings.Contains(err.Error(), "SECRET") {
		t.Errorf("network error leaks the token or is nil: %v", err)
	}
}

type fakeBot struct {
	mu    sync.Mutex
	sent  []string
	fails []error // returned in order before succeeding
}

func (f *fakeBot) Send(_ context.Context, html string, _ *Button) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.fails) > 0 {
		err := f.fails[0]
		f.fails = f.fails[1:]
		return err
	}
	f.sent = append(f.sent, html)
	return nil
}

func (f *fakeBot) sentSnapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.sent...)
}

func newApp(t *testing.T) core.App {
	t.Helper()
	app, err := tests.NewTestApp()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.Cleanup)
	if err := store.Sync(app); err != nil {
		t.Fatal(err)
	}
	return app
}

func messages(t *testing.T, app core.App, n int) []string {
	t.Helper()
	col, err := app.FindCollectionByNameOrId("messages")
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for i := range n {
		m := core.NewRecord(col)
		m.Set("direction", "inbound")
		m.Set("from_address", "s@x.test")
		m.Set("subject", "subject "+string(rune('A'+i)))
		if err := app.Save(m); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, m.Id)
	}
	return ids
}

func TestFlushIndividualThenSummary(t *testing.T) {
	app := newApp(t)
	bot := &fakeBot{}
	n := New(app, bot, "", nil)
	n.Flush(context.Background(), messages(t, app, 3))
	if len(bot.sent) != 3 {
		t.Fatalf("small burst sent %d alerts", len(bot.sent))
	}
	bot.sent = nil
	n.Flush(context.Background(), append(messages(t, app, 5), "purged-id"))
	if len(bot.sent) != 1 || !strings.Contains(bot.sent[0], "5 new messages") {
		t.Errorf("large burst = %v", bot.sent)
	}
}

func TestSendRetryRules(t *testing.T) {
	app := newApp(t)
	ids := messages(t, app, 1)

	limited := &fakeBot{fails: []error{&APIError{Code: 429, RetryAfter: time.Millisecond}}}
	New(app, limited, "", nil).Flush(context.Background(), ids)
	if len(limited.sent) != 1 {
		t.Error("rate-limited alert was not retried")
	}

	network := &fakeBot{fails: []error{errors.New("reset")}}
	start := time.Now()
	New(app, network, "", nil).Flush(context.Background(), ids)
	if len(network.sent) != 1 || time.Since(start) < time.Second {
		t.Error("network failure was not retried after a pause")
	}

	rejected := &fakeBot{fails: []error{&APIError{Code: 400, Description: "chat not found"}, nil}}
	New(app, rejected, "", nil).Flush(context.Background(), ids)
	if len(rejected.sent) != 0 || len(rejected.fails) != 1 {
		t.Error("a permanent API error was retried")
	}
}

func TestRunBatchesABurst(t *testing.T) {
	app := newApp(t)
	bot := &fakeBot{}
	n := New(app, bot, "", nil)
	n.Window = 50 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { n.Run(ctx); close(done) }()
	for _, id := range messages(t, app, 6) {
		n.Enqueue(id)
	}
	deadline := time.Now().Add(2 * time.Second)
	for len(bot.sentSnapshot()) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	if sent := bot.sentSnapshot(); len(sent) != 1 || !strings.Contains(sent[0], "6 new messages") {
		t.Errorf("burst produced %v", sent)
	}
}

func TestFormatDeliveryEscapes(t *testing.T) {
	text, button := FormatDelivery("bounced", "x@y.test", "<hi>", "550 <no such user>", "t9", "https://m.test")
	if strings.Contains(text, "<hi>") || strings.Contains(text, "<no such") || !strings.Contains(text, "Sent mail bounced") {
		t.Errorf("text = %s", text)
	}
	if button == nil || button.URL != "https://m.test/sent/t9" {
		t.Errorf("button = %+v", button)
	}
}

func TestWatchDeliveryAlertsOnBounceOnce(t *testing.T) {
	app := newApp(t)
	bot := &fakeBot{}
	New(app, bot, "", nil).WatchDelivery(app)
	id := messages(t, app, 1)[0]
	for _, st := range []string{"delivered", "bounced", "bounced"} {
		m, _ := app.FindRecordById("messages", id) // as the webhook does: fresh each event
		m.Set("delivery_status", st)
		if err := app.Save(m); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(2 * time.Second)
	for len(bot.sentSnapshot()) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	if sent := bot.sentSnapshot(); len(sent) != 1 || !strings.Contains(sent[0], "bounced") {
		t.Errorf("sent = %q", sent)
	}
}
