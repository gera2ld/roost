package outbound

import (
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/types"
)

// WebhookTolerance bounds clock skew for signed webhook deliveries.
const WebhookTolerance = 5 * time.Minute

// VerifyWebhook checks a Resend (Svix-format) signature: base64 HMAC-SHA256 of
// "<id>.<timestamp>.<body>" keyed with the secret after its "whsec_" prefix.
// The signature header may list several space-separated "v1,<sig>" values.
func VerifyWebhook(secret string, h http.Header, body []byte, now time.Time) error {
	id, ts, sigs := h.Get("svix-id"), h.Get("svix-timestamp"), h.Get("svix-signature")
	if id == "" || ts == "" || sigs == "" {
		return errors.New("missing signature headers")
	}
	sec, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return errors.New("bad timestamp")
	}
	if d := now.Sub(time.Unix(sec, 0)); d > WebhookTolerance || d < -WebhookTolerance {
		return errors.New("timestamp outside tolerance")
	}
	key, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(secret, "whsec_"))
	if err != nil {
		return fmt.Errorf("bad webhook secret: %w", err)
	}
	mac := hmac.New(sha256.New, key)
	fmt.Fprintf(mac, "%s.%s.", id, ts)
	mac.Write(body)
	want := mac.Sum(nil)
	for _, s := range strings.Fields(sigs) {
		version, value, ok := strings.Cut(s, ",")
		if !ok || version != "v1" {
			continue
		}
		got, err := base64.StdEncoding.DecodeString(value)
		if err == nil && hmac.Equal(got, want) {
			return nil
		}
	}
	return errors.New("signature mismatch")
}

type webhookEvent struct {
	Type      string `json:"type"`
	CreatedAt string `json:"created_at"`
	Data      struct {
		EmailID string `json:"email_id"`
		Bounce  *struct {
			Message string `json:"message"`
			Type    string `json:"type"`
		} `json:"bounce,omitempty"`
	} `json:"data"`
}

// deliveryStatus maps event types to messages.delivery_status. Later, stronger
// outcomes win: a bounce is never overwritten by a late "delayed".
var deliveryStatus = map[string]string{
	"email.delivery_delayed": "delayed",
	"email.delivered":        "delivered",
	"email.bounced":          "bounced",
	"email.complained":       "complained",
}

var statusRank = map[string]int{"": 0, "delayed": 1, "delivered": 2, "bounced": 3, "complained": 4}

// WebhookHandler records delivery events for sent messages. Each delivery is
// logged once (keyed on svix-id), so provider retries are harmless.
func WebhookHandler(secret string) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		body, err := io.ReadAll(io.LimitReader(e.Request.Body, 1<<20))
		if err != nil {
			return e.BadRequestError("read body", err)
		}
		if err := VerifyWebhook(secret, e.Request.Header, body, time.Now()); err != nil {
			return e.UnauthorizedError("invalid signature", nil)
		}
		var ev webhookEvent
		if err := json.Unmarshal(body, &ev); err != nil {
			return e.BadRequestError("invalid event", err)
		}
		if err := RecordEvent(e.App, e.Request.Header.Get("svix-id"), ev, body); err != nil {
			return err
		}
		return e.NoContent(http.StatusNoContent)
	}
}

// RecordEvent applies one provider event. Unknown messages are ignored (a
// purged message, or mail sent from elsewhere with the same key).
func RecordEvent(app core.App, eventID string, ev webhookEvent, raw []byte) error {
	if ev.Data.EmailID == "" {
		return nil
	}
	if _, err := app.FindFirstRecordByData("send_log", "event_id", eventID); err == nil {
		return nil // already applied
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	msg, err := app.FindFirstRecordByData("messages", "provider_id", ev.Data.EmailID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var detail any
	_ = json.Unmarshal(raw, &detail)
	errText := ""
	if ev.Data.Bounce != nil {
		errText = strings.TrimSpace(ev.Data.Bounce.Type + ": " + ev.Data.Bounce.Message)
	}
	return app.RunInTransaction(func(tx core.App) error {
		collection, err := tx.FindCollectionByNameOrId("send_log")
		if err != nil {
			return err
		}
		if err := saveLog(tx, collection, msg.Id, "event", strings.TrimPrefix(ev.Type, "email."),
			ev.Data.EmailID, errText, eventID, detail); err != nil {
			return err
		}
		next, ok := deliveryStatus[ev.Type]
		if !ok || statusRank[next] <= statusRank[msg.GetString("delivery_status")] {
			return nil
		}
		msg.Set("delivery_status", next)
		if at, err := types.ParseDateTime(ev.CreatedAt); err == nil && !at.IsZero() {
			msg.Set("delivery_at", at)
		} else {
			msg.Set("delivery_at", types.NowDateTime())
		}
		if errText != "" {
			msg.Set("send_error", errText)
		}
		return tx.Save(msg)
	})
}
