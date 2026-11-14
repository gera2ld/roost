package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ganigeorgiev.com/pocketbase/pocketbase/apis"
	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tests"

	"roost/server/internal/ingest"
	"roost/server/internal/outbound"
	"roost/server/internal/store"
	"roost/server/internal/threading"
)

type env struct {
	t       *testing.T
	app     core.App
	mux     http.Handler
	token   string
	mailDir string
	sent    *fakeSender
}

// fakeSender records what would go to Resend.
type fakeSender struct {
	emails []outbound.Email
	keys   []string
	err    error
}

func (f *fakeSender) Send(_ context.Context, email outbound.Email, key string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	f.emails = append(f.emails, email)
	f.keys = append(f.keys, key)
	return fmt.Sprintf("re_%d", len(f.emails)), nil
}

func newEnv(t *testing.T) *env {
	t.Helper()
	app, err := tests.NewTestApp()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.Cleanup)
	threading.Register(app)
	if err := store.Sync(app); err != nil {
		t.Fatal(err)
	}
	mailDir := t.TempDir()
	fixtures := filepath.Join("..", "mailparse", "testdata")
	if _, err := ingest.ImportFiles(context.Background(), app, mailDir, []string{
		filepath.Join(fixtures, "multipart-attachments.eml"),
		filepath.Join(fixtures, "plain-utf8.eml"),
	}, ""); err != nil {
		t.Fatal(err)
	}

	// The fixtures' domain sends; receive-only domains are tested on their own.
	dom, err := app.FindFirstRecordByData("domains", "domain", "roost.test")
	if err != nil {
		t.Fatal(err)
	}
	dom.Set("send_enabled", true)
	if err := app.Save(dom); err != nil {
		t.Fatal(err)
	}

	col, err := app.FindCollectionByNameOrId("users")
	if err != nil {
		t.Fatal(err)
	}
	user := core.NewRecord(col)
	user.SetEmail("owner@roost.test")
	user.SetPassword("correct horse battery")
	if err := app.Save(user); err != nil {
		t.Fatal(err)
	}
	token, err := user.NewAuthToken()
	if err != nil {
		t.Fatal(err)
	}

	r, err := apis.NewRouter(app)
	if err != nil {
		t.Fatal(err)
	}
	se := &core.ServeEvent{App: app, Router: r}
	r.BindFunc(SecurityHeaders)
	e := &env{t: t, app: app, token: token, mailDir: mailDir, sent: &fakeSender{}}
	Register(se, mailDir, &outbound.Service{App: app, Sender: e.sent, MailDir: mailDir})
	mux, err := r.BuildMux()
	if err != nil {
		t.Fatal(err)
	}
	e.mux = mux
	return e
}

func (e *env) do(method, path, body string, auth bool) *httptest.ResponseRecorder {
	e.t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if auth {
		req.Header.Set("Authorization", e.token)
	}
	rec := httptest.NewRecorder()
	e.mux.ServeHTTP(rec, req)
	return rec
}

func (e *env) message(messageID string) *core.Record {
	e.t.Helper()
	m, err := e.app.FindFirstRecordByData("messages", "message_id", messageID)
	if err != nil {
		e.t.Fatal(err)
	}
	return m
}

func (e *env) thread(id string) *core.Record {
	e.t.Helper()
	th, err := e.app.FindRecordById("threads", id)
	if err != nil {
		e.t.Fatal(err)
	}
	return th
}

func TestRequiresAuth(t *testing.T) {
	e := newEnv(t)
	thread := e.message("plain-1@example.com").GetString("thread")
	for _, c := range []struct{ method, path, body string }{
		{"POST", "/api/roost/threads/" + thread + "/actions", `{"action":"trash"}`},
		{"GET", "/api/roost/search?q=report", ""},
		{"GET", "/api/roost/mailboxes", ""},
	} {
		if rec := e.do(c.method, c.path, c.body, false); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s without auth = %d", c.method, c.path, rec.Code)
		}
	}
	if !e.thread(thread).GetDateTime("trashed_at").IsZero() {
		t.Error("unauthenticated trash took effect")
	}
}

func TestThreadReadAndFolders(t *testing.T) {
	e := newEnv(t)
	id := e.message("plain-1@example.com").GetString("thread")
	path := "/api/roost/threads/" + id + "/actions"

	if rec := e.do("POST", path, `{"action":"read"}`, true); rec.Code != 200 {
		t.Fatalf("read = %d %s", rec.Code, rec.Body)
	}
	if n := e.thread(id).GetInt("unread_count"); n != 0 {
		t.Errorf("unread after read = %d", n)
	}
	e.do("POST", path, `{"action":"unread"}`, true)
	if n := e.thread(id).GetInt("unread_count"); n != 1 {
		t.Errorf("unread after unread = %d", n)
	}

	e.do("POST", path, `{"action":"archive"}`, true)
	if !e.thread(id).GetBool("is_archived") {
		t.Error("archive did not stick")
	}
	e.do("POST", path, `{"action":"trash"}`, true)
	if e.thread(id).GetDateTime("trashed_at").IsZero() {
		t.Error("trash did not stick")
	}
	e.do("POST", path, `{"action":"restore"}`, true)
	if !e.thread(id).GetDateTime("trashed_at").IsZero() {
		t.Error("restore did not clear trashed_at")
	}
	if rec := e.do("POST", path, `{"action":"explode"}`, true); rec.Code != http.StatusBadRequest {
		t.Errorf("unknown action = %d", rec.Code)
	}
}

func TestPurgeOnlyFromTrash(t *testing.T) {
	e := newEnv(t)
	msg := e.message("plain-1@example.com")
	id := msg.GetString("thread")
	raw := filepath.Join(e.mailDir, filepath.FromSlash(msg.GetString("raw_path")))
	path := "/api/roost/threads/" + id + "/actions"

	if rec := e.do("POST", path, `{"action":"purge"}`, true); rec.Code != http.StatusBadRequest {
		t.Fatalf("purge outside trash = %d", rec.Code)
	}
	e.do("POST", path, `{"action":"trash"}`, true)
	if rec := e.do("POST", path, `{"action":"purge"}`, true); rec.Code != http.StatusNoContent {
		t.Fatalf("purge = %d %s", rec.Code, rec.Body)
	}
	if _, err := e.app.FindRecordById("threads", id); err == nil {
		t.Error("thread survived purge")
	}
	if _, err := os.Stat(raw); !os.IsNotExist(err) {
		t.Errorf("raw file survived purge: %v", err)
	}
}

func TestMessageStar(t *testing.T) {
	e := newEnv(t)
	msg := e.message("multi-1@example.com")
	if rec := e.do("POST", "/api/roost/messages/"+msg.Id+"/actions", `{"action":"star"}`, true); rec.Code != 200 {
		t.Fatalf("star = %d %s", rec.Code, rec.Body)
	}
	if !e.thread(msg.GetString("thread")).GetBool("has_starred") {
		t.Error("thread has_starred not derived from star")
	}
}

func TestSearchExcludesTrash(t *testing.T) {
	e := newEnv(t)
	search := func(q string) []map[string]any {
		rec := e.do("GET", "/api/roost/search?q="+q, "", true)
		var out []map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("search %q: %d %s", q, rec.Code, rec.Body)
		}
		return out
	}
	hits := search("attached")
	if len(hits) != 1 || hits[0]["subject"] != "Report attached" {
		t.Fatalf("search = %v", hits)
	}
	id := hits[0]["id"].(string)
	e.do("POST", "/api/roost/threads/"+id+"/actions", `{"action":"trash"}`, true)
	if hits := search("attached"); len(hits) != 0 {
		t.Errorf("trashed thread in search: %v", hits)
	}
}

func TestMailboxes(t *testing.T) {
	e := newEnv(t)
	rec := e.do("GET", "/api/roost/mailboxes", "", true)
	var boxes []Mailbox
	if err := json.Unmarshal(rec.Body.Bytes(), &boxes); err != nil {
		t.Fatal(err, rec.Body)
	}
	if len(boxes) != 1 || boxes[0].Address != "me@roost.test" || boxes[0].Total != 2 || boxes[0].Unread != 2 {
		t.Errorf("mailboxes = %+v", boxes)
	}
}

func TestAttachmentDownload(t *testing.T) {
	e := newEnv(t)
	msg := e.message("multi-1@example.com")
	atts, err := e.app.FindRecordsByFilter("attachments", "message = {:m}", "", 0, 0, map[string]any{"m": msg.Id})
	if err != nil || len(atts) == 0 {
		t.Fatalf("attachments: %v %d", err, len(atts))
	}
	var evil *core.Record
	for _, a := range atts {
		if strings.HasSuffix(a.GetString("filename"), "evil.sh") {
			evil = a
		}
	}
	if evil == nil {
		t.Fatal("evil.sh attachment row missing")
	}
	if rec := e.do("GET", "/api/roost/attachments/"+evil.Id, "", false); rec.Code != http.StatusUnauthorized {
		t.Errorf("anonymous download = %d", rec.Code)
	}
	rec := e.do("GET", "/api/roost/attachments/"+evil.Id, "", true)
	if rec.Code != 200 || rec.Body.String() != "#!/bin/sh\necho hi\n" {
		t.Fatalf("download = %d %q", rec.Code, rec.Body)
	}
	h := rec.Header()
	if h.Get("Content-Type") != "application/octet-stream" {
		t.Errorf("content-type = %q", h.Get("Content-Type"))
	}
	if cd := h.Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment;") || strings.Contains(cd, "/") {
		t.Errorf("content-disposition = %q", cd)
	}
	if !strings.Contains(h.Get("Content-Security-Policy"), "sandbox") {
		t.Errorf("csp = %q", h.Get("Content-Security-Policy"))
	}
}

func TestContentDisposition(t *testing.T) {
	got := ContentDisposition(`报告 "q".pdf`)
	want := `attachment; filename="__ _q_.pdf"; filename*=UTF-8''%E6%8A%A5%E5%91%8A%20%22q%22.pdf`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestSecurityHeaders(t *testing.T) {
	e := newEnv(t)
	h := e.do("GET", "/api/roost/mailboxes", "", true).Header()
	if h.Get("X-Frame-Options") != "DENY" || h.Get("Referrer-Policy") != "no-referrer" ||
		!strings.Contains(h.Get("Content-Security-Policy"), "frame-ancestors 'none'") {
		t.Errorf("headers = %v", h)
	}
}
