package outbound

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestParseList(t *testing.T) {
	got := ParseList(`"Doe, Jane" <Jane@X.test>, bob@y.test; not-an-address`)
	want := []Address{{Name: "Doe, Jane", Address: "jane@x.test"}, {Address: "bob@y.test"}, {Address: "not-an-address"}}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
	if Valid(got[2]) == nil {
		t.Error("garbage token validated")
	}
	if len(ParseList("  ")) != 0 {
		t.Error("blank field produced addresses")
	}
}

// Svix's published example vector: Resend signs webhooks with Svix.
func TestVerifyWebhookSvixVector(t *testing.T) {
	secret := "whsec_MfKQ9r8GKYqrTwjUPD8ILPZIo2LaLaSw"
	h := http.Header{}
	h.Set("svix-id", "msg_p5jXN8AQM9LWM0D4loKWxJek")
	h.Set("svix-timestamp", "1614265330")
	h.Set("svix-signature", "v1,bm9wZQ== v1,g0hM9SsE+OTPJTGt/tmIKtSyZlE3uFJELVlNIOLJ1OE=")
	body := []byte(`{"test": 2432232314}`)
	at := time.Unix(1614265330, 0)
	if err := VerifyWebhook(secret, h, body, at); err != nil {
		t.Fatalf("valid signature rejected: %v", err)
	}
	if VerifyWebhook(secret, h, []byte(`{"test": 1}`), at) == nil {
		t.Error("tampered body accepted")
	}
	if VerifyWebhook(secret, h, body, at.Add(10*time.Minute)) == nil {
		t.Error("stale timestamp accepted")
	}
	h.Set("svix-signature", "v2,g0hM9SsE+OTPJTGt/tmIKtSyZlE3uFJELVlNIOLJ1OE=")
	if VerifyWebhook(secret, h, body, at) == nil {
		t.Error("unknown signature version accepted")
	}
}

func TestResendRequest(t *testing.T) {
	var got map[string]any
	var header http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header = r.Header
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &got)
		if r.URL.Path != "/emails" {
			w.WriteHeader(404)
			return
		}
		_, _ = w.Write([]byte(`{"id":"re_123"}`))
	}))
	defer srv.Close()
	c := &Resend{APIKey: "re_key", BaseURL: srv.URL}
	id, err := c.Send(context.Background(), Email{
		From: "Me <me@x.test>", To: []string{"a@y.test"}, Subject: "Hi", Text: "Body",
		Headers:     map[string]string{"Message-ID": "<m@x.test>"},
		Attachments: []Attachment{{Filename: "a.txt", ContentType: "text/plain", Content: []byte("hello")}},
	}, "idem-1")
	if err != nil || id != "re_123" {
		t.Fatalf("send = %q, %v", id, err)
	}
	if header.Get("Authorization") != "Bearer re_key" || header.Get("Idempotency-Key") != "idem-1" {
		t.Errorf("headers = %v", header)
	}
	att := got["attachments"].([]any)[0].(map[string]any)
	if att["content"] != base64.StdEncoding.EncodeToString([]byte("hello")) || att["filename"] != "a.txt" {
		t.Errorf("attachment = %v", att)
	}
	if got["headers"].(map[string]any)["Message-ID"] != "<m@x.test>" {
		t.Errorf("headers = %v", got["headers"])
	}
	if _, ok := got["cc"]; ok {
		t.Error("empty cc should be omitted")
	}
}

func TestResendError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(422)
		_, _ = w.Write([]byte(`{"statusCode":422,"name":"validation_error","message":"Invalid from"}`))
	}))
	defer srv.Close()
	_, err := (&Resend{APIKey: "k", BaseURL: srv.URL}).Send(context.Background(), Email{}, "")
	apiErr, ok := err.(*APIError)
	if !ok || apiErr.Status != 422 || apiErr.Name != "validation_error" || apiErr.Message != "Invalid from" {
		t.Fatalf("err = %#v", err)
	}
}
