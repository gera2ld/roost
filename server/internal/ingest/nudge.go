package ingest

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"time"

	"ganigeorgiev.com/pocketbase/pocketbase/core"

	"roost/server/internal/spool"
)

// Nudge request signing (keep in sync with worker/src/nudge.ts):
//
//	X-Roost-Timestamp: <unix seconds>
//	X-Roost-Signature: hex(HMAC-SHA256(secret, "<timestamp>.<raw body>"))
const (
	HeaderTimestamp = "X-Roost-Timestamp"
	HeaderSignature = "X-Roost-Signature"
	maxClockSkew    = 5 * time.Minute
	maxNudgeBody    = 4 << 10
)

// Sign returns the signature for body at timestamp ts.
func Sign(secret []byte, ts string, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(ts))
	mac.Write([]byte("."))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// Verify checks a nudge signature and that its timestamp is recent.
func Verify(secret []byte, ts, signature string, body []byte, now time.Time) bool {
	sec, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return false
	}
	if d := now.Sub(time.Unix(sec, 0)); d > maxClockSkew || d < -maxClockSkew {
		return false
	}
	want := Sign(secret, ts, body)
	return hmac.Equal([]byte(want), []byte(signature))
}

// NudgeHandler accepts {"key": "..."} from the Worker and ingests it in the
// background. It answers 202 at once: the Worker never waits for ingest, and the
// sweep covers any nudge that is lost or fails.
func NudgeHandler(in *Ingester, secret []byte) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		body, err := io.ReadAll(io.LimitReader(e.Request.Body, maxNudgeBody+1))
		if err != nil || len(body) > maxNudgeBody {
			return e.BadRequestError("invalid body", nil)
		}
		if !Verify(secret, e.Request.Header.Get(HeaderTimestamp), e.Request.Header.Get(HeaderSignature), body, time.Now()) {
			return e.UnauthorizedError("bad signature", nil)
		}
		var req struct {
			Key string `json:"key"`
		}
		if err := json.Unmarshal(body, &req); err != nil || !spool.ValidKey(req.Key) {
			return e.BadRequestError("invalid key", nil)
		}
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			_, _ = in.Ingest(ctx, req.Key) // failures are recorded and retried by the sweep
		}()
		return e.NoContent(http.StatusAccepted)
	}
}
