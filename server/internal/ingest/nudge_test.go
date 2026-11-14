package ingest

import (
	"strconv"
	"testing"
	"time"
)

func TestVerify(t *testing.T) {
	secret := []byte("s3cret")
	now := time.Unix(1_800_000_000, 0)
	ts := strconv.FormatInt(now.Unix(), 10)
	body := []byte(`{"key":"x"}`)
	sig := Sign(secret, ts, body)

	if !Verify(secret, ts, sig, body, now) {
		t.Fatal("valid signature rejected")
	}
	if Verify([]byte("other"), ts, sig, body, now) {
		t.Error("wrong secret accepted")
	}
	if Verify(secret, ts, sig, []byte(`{"key":"y"}`), now) {
		t.Error("tampered body accepted")
	}
	if Verify(secret, ts, sig, body, now.Add(10*time.Minute)) {
		t.Error("stale timestamp accepted")
	}
	if Verify(secret, "nope", sig, body, now) {
		t.Error("bad timestamp accepted")
	}
}

// TestSignMatchesWorker pins the signature format shared with worker/src/nudge.ts.
func TestSignMatchesWorker(t *testing.T) {
	got := Sign([]byte("test-secret"), "1800000000", []byte(`{"key":"2026/11/13/093000-00000000-0000-4000-8000-000000000001.eml"}`))
	const want = "79c0fe375034cfcaa93edc4bae5bd7d95045bf058c206e1a3cc6a8091d402e91"
	if got != want {
		t.Fatalf("signature = %s, want %s", got, want)
	}
}
