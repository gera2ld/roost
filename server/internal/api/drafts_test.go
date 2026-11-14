package api

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"ganigeorgiev.com/pocketbase/pocketbase/core"

	"roost/server/internal/ingest"
	"roost/server/internal/outbound"
)

func (e *env) json(method, path, body string) (int, map[string]any) {
	e.t.Helper()
	rec := e.do(method, path, body, true)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func (e *env) draft(mode, source string) map[string]any {
	e.t.Helper()
	code, d := e.json("POST", "/api/roost/drafts",
		fmt.Sprintf(`{"mode":%q,"source":%q,"tz":"Asia/Shanghai"}`, mode, source))
	if code != http.StatusCreated {
		e.t.Fatalf("create %s draft = %d %v", mode, code, d)
	}
	return d
}

func addresses(v any) []string {
	var out []string
	for _, a := range v.([]any) {
		out = append(out, a.(map[string]any)["address"].(string))
	}
	return out
}

func TestReplyDraftPrefill(t *testing.T) {
	e := newEnv(t)
	src := e.message("plain-1@example.com")
	d := e.draft("reply", src.Id)
	if d["from_address"] != "me@roost.test" || d["subject"] != "Re: Café meeting" ||
		d["in_reply_to"] != "plain-1@example.com" || d["send_status"] != "draft" || d["thread"] != "" {
		t.Errorf("reply draft = %v", d)
	}
	if got := addresses(d["to"]); len(got) != 1 || got[0] != "zhang@example.com" {
		t.Errorf("reply to = %v", got)
	}
	body := d["text_body"].(string)
	if !strings.Contains(body, "wrote:\n> Hello, see you at the café.") || !strings.Contains(body, "Nov 13, 2026 at 9:30 AM") {
		t.Errorf("quote = %q", body)
	}

	all := e.draft("reply_all", src.Id)
	if got := addresses(all["to"]); strings.Join(got, ",") != "zhang@example.com,other@roost.test" {
		t.Errorf("reply-all to = %v (own address must be dropped)", got)
	}
}

func TestSendReplyJoinsThread(t *testing.T) {
	e := newEnv(t)
	src := e.message("plain-1@example.com")
	d := e.draft("reply", src.Id)
	id := d["id"].(string)
	if code, _ := e.json("PATCH", "/api/roost/drafts/"+id,
		`{"from_name":"Me","to":"Zhang <zhang@example.com>, bcc-me@else.test","bcc":"hidden@else.test","text_body":"Sure.\n"}`); code != 200 {
		t.Fatalf("patch = %d", code)
	}
	code, sent := e.json("POST", "/api/roost/drafts/"+id+"/send", "")
	if code != 200 {
		t.Fatalf("send = %d %v", code, sent)
	}
	if sent["send_status"] != "sent" || sent["provider_id"] != "re_1" || sent["thread"] != src.GetString("thread") {
		t.Errorf("sent = %v", sent)
	}
	email := e.sent.emails[0]
	if email.From != `"Me" <me@roost.test>` || len(email.To) != 2 || email.Bcc[0] != "hidden@else.test" {
		t.Errorf("email = %+v", email)
	}
	mid := sent["message_id"].(string)
	if email.Headers["Message-ID"] != "<"+mid+">" || !strings.HasSuffix(mid, "@roost.test") ||
		email.Headers["In-Reply-To"] != "<plain-1@example.com>" {
		t.Errorf("headers = %v", email.Headers)
	}
	raw, err := os.ReadFile(filepath.Join(e.mailDir, filepath.FromSlash(sent["raw_path"].(string))))
	if err != nil || strings.Contains(string(raw), "hidden@else.test") || !strings.Contains(string(raw), "In-Reply-To: <plain-1@example.com>") {
		t.Errorf("raw copy (err %v):\n%s", err, raw)
	}
	thread := e.thread(src.GetString("thread"))
	if !thread.GetBool("has_outbound") || thread.GetInt("message_count") != 2 {
		t.Errorf("thread after send: outbound=%v count=%d", thread.GetBool("has_outbound"), thread.GetInt("message_count"))
	}
	// A sent message is no longer a draft.
	if code, _ := e.json("PATCH", "/api/roost/drafts/"+id, `{"subject":"x"}`); code != 404 {
		t.Errorf("patch after send = %d", code)
	}
	if n := countLog(t, e.app, id, "attempt"); n != 1 {
		t.Errorf("attempt log rows = %d", n)
	}
}

func TestForwardCarriesAttachments(t *testing.T) {
	e := newEnv(t)
	src := e.message("multi-1@example.com")
	d := e.draft("forward", src.Id)
	if d["subject"] != "Fwd: Report attached" || len(d["forward_attachments"].([]any)) != 2 {
		t.Fatalf("forward draft = %v", d)
	}
	id := d["id"].(string)
	e.json("PATCH", "/api/roost/drafts/"+id, `{"to":"friend@else.test"}`)
	if code, sent := e.json("POST", "/api/roost/drafts/"+id+"/send", ""); code != 200 {
		t.Fatalf("send = %d %v", code, sent)
	}
	atts := e.sent.emails[0].Attachments
	if len(atts) != 2 || string(atts[0].Content) != "#!/bin/sh\necho hi\n" || atts[0].Filename != "evil.sh" {
		t.Fatalf("attachments = %+v", atts)
	}
	rows, _ := e.app.FindRecordsByFilter("attachments", "message = {:m}", "", 0, 0, map[string]any{"m": id})
	if len(rows) != 2 {
		t.Errorf("sent message attachment rows = %d", len(rows))
	}
	// The stored copy serves downloads like received mail.
	if rec := e.do("GET", "/api/roost/attachments/"+rows[0].Id, "", true); rec.Code != 200 {
		t.Errorf("download from sent copy = %d", rec.Code)
	}
}

func TestSendValidation(t *testing.T) {
	e := newEnv(t)
	d := e.draft("new", "")
	id := d["id"].(string)
	send := func() (int, map[string]any) { return e.json("POST", "/api/roost/drafts/"+id+"/send", "") }

	e.json("PATCH", "/api/roost/drafts/"+id, `{"from_address":"me@roost.test"}`)
	if code, out := send(); code != 400 || !strings.Contains(fmt.Sprint(out["message"]), "recipient") {
		t.Errorf("no recipients = %d %v", code, out)
	}
	e.json("PATCH", "/api/roost/drafts/"+id, `{"to":"a@x.test, oops"}`)
	if code, _ := send(); code != 400 {
		t.Errorf("bad address = %d", code)
	}
	e.json("PATCH", "/api/roost/drafts/"+id, `{"to":"a@x.test","from_address":"me@gmail.com"}`)
	if code, out := send(); code != 400 || !strings.Contains(fmt.Sprint(out["message"]), "not one of your domains") {
		t.Errorf("foreign From = %d %v", code, out)
	}
	if len(e.sent.emails) != 0 {
		t.Error("invalid drafts reached the provider")
	}
}

func TestSendFailureKeepsDraft(t *testing.T) {
	e := newEnv(t)
	e.sent.err = errors.New("connection reset")
	d := e.draft("reply", e.message("plain-1@example.com").Id)
	id := d["id"].(string)
	if code, _ := e.json("POST", "/api/roost/drafts/"+id+"/send", ""); code != http.StatusBadGateway {
		t.Fatalf("send = %d", code)
	}
	msg, _ := e.app.FindRecordById("messages", id)
	if msg.GetString("send_status") != "failed" || msg.GetString("send_error") != "connection reset" || msg.GetString("thread") != "" {
		t.Errorf("after failure: %v", msg.FieldsData())
	}
	firstKey := msg.GetString("message_id")
	e.sent.err = nil
	if code, _ := e.json("POST", "/api/roost/drafts/"+id+"/send", ""); code != 200 {
		t.Fatalf("retry = %d", code)
	}
	if e.sent.keys[0] != id+"/"+firstKey {
		t.Errorf("retry used idempotency key %q, want the first attempt's", e.sent.keys[0])
	}
	if n := countLog(t, e.app, id, "attempt"); n != 2 {
		t.Errorf("attempt rows = %d", n)
	}
}

func TestDeleteDraft(t *testing.T) {
	e := newEnv(t)
	id := e.draft("new", "")["id"].(string)
	if rec := e.do("DELETE", "/api/roost/drafts/"+id, "", true); rec.Code != 204 {
		t.Fatalf("delete = %d", rec.Code)
	}
	inbound := e.message("plain-1@example.com").Id
	if rec := e.do("DELETE", "/api/roost/drafts/"+inbound, "", true); rec.Code != 404 {
		t.Errorf("deleting received mail via drafts = %d", rec.Code)
	}
}

const testWebhookSecret = "whsec_dGVzdC1zZWNyZXQtZm9yLXdlYmhvb2tz"

func signed(t *testing.T, id, body string) *http.Request {
	t.Helper()
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	key, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(testWebhookSecret, "whsec_"))
	mac := hmac.New(sha256.New, key)
	fmt.Fprintf(mac, "%s.%s.%s", id, ts, body)
	req := httptest.NewRequest("POST", "/api/roost/resend/webhook", strings.NewReader(body))
	req.Header.Set("svix-id", id)
	req.Header.Set("svix-timestamp", ts)
	req.Header.Set("svix-signature", "v1,"+base64.StdEncoding.EncodeToString(mac.Sum(nil)))
	return req
}

func TestWebhookDeliveryEvents(t *testing.T) {
	e := newEnv(t)
	d := e.draft("reply", e.message("plain-1@example.com").Id)
	id := d["id"].(string)
	e.json("POST", "/api/roost/drafts/"+id+"/send", "")

	handler := outbound.WebhookHandler(testWebhookSecret)
	deliver := func(eventID, typ string) int {
		body := fmt.Sprintf(`{"type":%q,"created_at":"2026-11-14T10:00:00.000Z","data":{"email_id":"re_1","bounce":{"type":"Permanent","message":"mailbox full"}}}`, typ)
		rec := httptest.NewRecorder()
		ev := &core.RequestEvent{App: e.app}
		ev.Request, ev.Response = signed(t, eventID, body), rec
		if err := handler(ev); err != nil {
			t.Fatalf("%s: %v", typ, err)
		}
		return rec.Code
	}
	status := func() string {
		m, _ := e.app.FindRecordById("messages", id)
		return m.GetString("delivery_status")
	}
	deliver("evt_1", "email.delivered")
	if status() != "delivered" {
		t.Errorf("after delivered: %q", status())
	}
	deliver("evt_2", "email.delivery_delayed") // late, weaker
	if status() != "delivered" {
		t.Errorf("delayed overwrote delivered: %q", status())
	}
	deliver("evt_3", "email.bounced")
	deliver("evt_3", "email.bounced") // provider retry
	if status() != "bounced" {
		t.Errorf("after bounce: %q", status())
	}
	if n := countLog(t, e.app, id, "event"); n != 3 {
		t.Errorf("event rows = %d, want 3 (retry deduped)", n)
	}

	bad := signed(t, "evt_4", `{}`)
	bad.Header.Set("svix-signature", "v1,AAAA")
	ev := &core.RequestEvent{App: e.app}
	ev.Request, ev.Response = bad, httptest.NewRecorder()
	if err := handler(ev); err == nil {
		t.Error("bad signature accepted")
	}
}

// Resend delivers with its own Message-ID. A reply to that id which arrives before
// the webhook is folded into the thread once the webhook reports it, and later
// replies from Roost reference the delivered id.
func TestWebhookAdoptsDeliveredMessageID(t *testing.T) {
	e := newEnv(t)
	src := e.message("plain-1@example.com")
	d := e.draft("reply", src.Id)
	id := d["id"].(string)
	e.json("POST", "/api/roost/drafts/"+id+"/send", "")
	const delivered = "0106-abc-000000@ap-northeast-1.amazonses.com"

	reply := filepath.Join(t.TempDir(), "reply.eml")
	if err := os.WriteFile(reply, []byte("From: Zhang <zhang@example.com>\r\nTo: me@roost.test\r\n"+
		"Subject: Re: unrelated words\r\nMessage-ID: <their-reply@example.com>\r\n"+
		"In-Reply-To: <"+delivered+">\r\nReferences: <"+delivered+">\r\n"+
		"Date: Sat, 14 Nov 2026 11:00:00 +0000\r\n\r\nThanks!\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ingest.ImportFiles(context.Background(), e.app, e.mailDir, []string{reply}, ""); err != nil {
		t.Fatal(err)
	}
	if e.message("their-reply@example.com").GetString("thread") == src.GetString("thread") {
		t.Fatal("reply joined the thread before its parent's delivered id was known")
	}

	body := `{"type":"email.sent","created_at":"2026-11-14T10:00:00.000Z","data":{"email_id":"re_1","message_id":"<` + delivered + `>"}}`
	ev := &core.RequestEvent{App: e.app}
	ev.Request, ev.Response = signed(t, "evt_sent", body), httptest.NewRecorder()
	if err := outbound.WebhookHandler(testWebhookSecret)(ev); err != nil {
		t.Fatal(err)
	}

	sent, _ := e.app.FindRecordById("messages", id)
	if sent.GetString("message_id") != delivered {
		t.Fatalf("message_id = %q, want the delivered id", sent.GetString("message_id"))
	}
	if got := e.message("their-reply@example.com").GetString("thread"); got != src.GetString("thread") {
		t.Errorf("reply thread = %q, want %q", got, src.GetString("thread"))
	}
	if n := e.thread(src.GetString("thread")).GetInt("message_count"); n != 3 {
		t.Errorf("thread message_count = %d, want 3", n)
	}
	if next := e.draft("reply", id); next["in_reply_to"] != delivered {
		t.Errorf("reply to sent: in_reply_to = %v", next["in_reply_to"])
	}
}

func countLog(t *testing.T, app core.App, message, kind string) int {
	t.Helper()
	rows, err := app.FindRecordsByFilter("send_log", "message = {:m} && kind = {:k}", "", 0, 0,
		map[string]any{"m": message, "k": kind})
	if err != nil {
		t.Fatal(err)
	}
	return len(rows)
}
