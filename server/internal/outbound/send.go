package outbound

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/types"
	"github.com/jhillyerd/enmime/v2"

	"roost/server/internal/ingest"
	"roost/server/internal/mailparse"
	"roost/server/internal/spool"
	"roost/server/internal/threading"
)

// MaxAttachmentBytes bounds what one email carries (Resend allows 40 MB per
// email after base64; this leaves room for the encoding).
const MaxAttachmentBytes = 25 << 20

// ErrValidation marks a draft that cannot be sent as it stands.
var ErrValidation = errors.New("cannot send")

// Service sends drafts. It is safe for concurrent use; sends are serialized.
type Service struct {
	App     core.App
	Sender  Sender
	MailDir string
	Logger  *slog.Logger
	Now     func() time.Time

	mu sync.Mutex
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) logger() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}

// Editable reports whether a message is a draft the owner may still change.
func Editable(rec *core.Record) bool {
	st := rec.GetString("send_status")
	return rec.GetString("direction") == "outbound" && (st == "draft" || st == "failed")
}

// Send delivers a draft. On success the message becomes a sent message in its
// thread; on a provider error it is marked failed (and stays a draft) with the
// error recorded. Every attempt lands in send_log.
func (s *Service) Send(ctx context.Context, id string) (*core.Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	msg, err := s.App.FindRecordById("messages", id)
	if err != nil {
		return nil, fmt.Errorf("%w: draft not found", ErrValidation)
	}
	if !Editable(msg) {
		return nil, fmt.Errorf("%w: message is not a draft", ErrValidation)
	}
	from, to, cc, bcc, err := s.validate(msg)
	if err != nil {
		return nil, err
	}
	attachments, err := s.forwarded(msg)
	if err != nil {
		return nil, err
	}

	domain := ingest.DomainOf(from.Address)
	if mid := msg.GetString("message_id"); mid == "" || ingest.DomainOf(mid) != domain {
		msg.Set("message_id", newMessageID(msg.Id, domain))
	}
	now := s.now().UTC()
	raw, err := buildMIME(msg, from, to, cc, bcc, attachments, now)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrValidation, err)
	}

	msg.Set("send_status", "sending")
	msg.Set("send_error", "")
	if err := s.App.Save(msg); err != nil {
		return nil, err
	}

	email := Email{
		From:        Format(from),
		To:          formatAll(to),
		Cc:          formatAll(cc),
		Bcc:         formatAll(bcc),
		Subject:     msg.GetString("subject"),
		Text:        msg.GetString("text_body"),
		Headers:     threadHeaders(msg),
		Attachments: attachments,
	}
	// Keyed on the record and its Message-ID: a retry after a timeout cannot send twice.
	providerID, sendErr := s.Sender.Send(ctx, email, msg.Id+"/"+msg.GetString("message_id"))
	if sendErr != nil {
		msg.Set("send_status", "failed")
		msg.Set("send_error", sendErr.Error())
		err := errors.Join(s.App.Save(msg), s.log(msg.Id, "attempt", "failed", "", sendErr.Error(), "", nil))
		if err != nil {
			s.logger().Error("record failed send", "message", msg.Id, "error", err)
		}
		return msg, sendErr
	}

	if err := s.storeSent(msg, raw, providerID, from, append(append(append([]Address{}, to...), cc...), bcc...), now); err != nil {
		// The provider accepted the mail: never leave it looking unsent. Start
		// from the stored row so whatever made the full save fail is not re-sent.
		s.logger().Error("store sent message", "message", msg.Id, "provider_id", providerID, "error", err)
		if serr := markSent(s.App, msg.Id, providerID, now); serr != nil {
			s.logger().Error("mark sent", "message", msg.Id, "error", serr)
		}
	}
	if err := s.log(msg.Id, "attempt", "sent", providerID, "", "", nil); err != nil {
		s.logger().Error("send log", "message", msg.Id, "error", err)
	}
	return s.App.FindRecordById("messages", msg.Id)
}

func (s *Service) validate(msg *core.Record) (from Address, to, cc, bcc []Address, err error) {
	fail := func(format string, args ...any) error {
		return fmt.Errorf("%w: "+format, append([]any{ErrValidation}, args...)...)
	}
	from = Address{Name: strings.TrimSpace(msg.GetString("from_name")), Address: strings.ToLower(msg.GetString("from_address"))}
	if err := Valid(from); err != nil {
		return from, nil, nil, nil, fail("choose a From address")
	}
	dom, derr := s.App.FindFirstRecordByData("domains", "domain", ingest.DomainOf(from.Address))
	if derr != nil {
		return from, nil, nil, nil, fail("%s is not one of your domains", ingest.DomainOf(from.Address))
	}
	if !dom.GetBool("send_enabled") {
		return from, nil, nil, nil, fail("%s is receive-only; turn on sending for it in Settings", dom.GetString("domain"))
	}
	for _, f := range []struct {
		name string
		dst  *[]Address
	}{{"to", &to}, {"cc", &cc}, {"bcc", &bcc}} {
		_ = msg.UnmarshalJSONField(f.name, f.dst)
		for _, a := range *f.dst {
			if err := Valid(a); err != nil {
				return from, nil, nil, nil, fail("%v", err)
			}
		}
	}
	if len(to)+len(cc)+len(bcc) == 0 {
		return from, nil, nil, nil, fail("add a recipient")
	}
	return from, to, cc, bcc, nil
}

// forwarded loads the attachments a forward carries over, from the original's raw EML.
func (s *Service) forwarded(msg *core.Record) ([]Attachment, error) {
	var ids []string
	_ = msg.UnmarshalJSONField("forward_attachments", &ids)
	var out []Attachment
	total := 0
	raws := map[string][]byte{}
	for _, id := range ids {
		att, err := s.App.FindRecordById("attachments", id)
		if err != nil {
			continue // the original was purged: send without it
		}
		src, err := s.App.FindRecordById("messages", att.GetString("message"))
		if err != nil {
			continue
		}
		key := src.GetString("raw_path")
		raw, ok := raws[key]
		if !ok {
			if !spool.ValidKey(key) {
				continue
			}
			if raw, err = os.ReadFile(filepath.Join(s.MailDir, filepath.FromSlash(key))); err != nil {
				return nil, fmt.Errorf("read forwarded message: %w", err)
			}
			raws[key] = raw
		}
		content, err := mailparse.ExtractPart(raw, att.GetString("part_id"))
		if err != nil {
			return nil, fmt.Errorf("extract %s: %w", att.GetString("filename"), err)
		}
		total += len(content)
		if total > MaxAttachmentBytes {
			return nil, fmt.Errorf("%w: attachments exceed %d MB", ErrValidation, MaxAttachmentBytes>>20)
		}
		ct := att.GetString("content_type")
		if ct == "" {
			ct = "application/octet-stream"
		}
		out = append(out, Attachment{
			Filename:    mailparse.SanitizeFilename(att.GetString("filename"), 0),
			ContentType: ct,
			Content:     content,
		})
	}
	return out, nil
}

func threadHeaders(msg *core.Record) map[string]string {
	h := map[string]string{"Message-ID": "<" + msg.GetString("message_id") + ">"}
	if irt := msg.GetString("in_reply_to"); irt != "" {
		h["In-Reply-To"] = "<" + irt + ">"
	}
	var refs []string
	_ = msg.UnmarshalJSONField("references", &refs)
	if len(refs) > 0 {
		h["References"] = "<" + strings.Join(refs, "> <") + ">"
	}
	return h
}

// buildMIME renders the copy Roost keeps of what it sent (Bcc stays in the
// database only, as on the wire).
func buildMIME(msg *core.Record, from Address, to, cc, bcc []Address, atts []Attachment, now time.Time) ([]byte, error) {
	b := enmime.Builder().
		From(from.Name, from.Address).
		ToAddrs(mailAddrs(to)).
		CCAddrs(mailAddrs(cc)).
		BCCAddrs(mailAddrs(bcc)). // counted, never written as a header
		Subject(msg.GetString("subject")).
		Date(now).
		Text([]byte(msg.GetString("text_body")))
	if len(to)+len(cc) == 0 {
		b = b.Header("To", "undisclosed-recipients:;")
	}
	for k, v := range threadHeaders(msg) {
		b = b.Header(k, v)
	}
	for _, a := range atts {
		b = b.AddAttachment(a.Content, a.ContentType, a.Filename)
	}
	part, err := b.Build()
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := part.Encode(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (s *Service) storeSent(msg *core.Record, raw []byte, providerID string, from Address, recipients []Address, now time.Time) error {
	key := spool.KeyPrefixAt(now) + "-" + newUUID() + ".eml"
	if err := ingest.WriteRaw(s.MailDir, key, raw); err != nil {
		return err
	}
	parsed, err := mailparse.Parse(raw)
	if err != nil {
		return err
	}
	at, err := types.ParseDateTime(now)
	if err != nil {
		return err
	}
	return s.App.RunInTransaction(func(tx core.App) error {
		msg.Set("send_status", "sent")
		msg.Set("provider_id", providerID)
		msg.Set("raw_path", key)
		msg.Set("raw_size", len(raw))
		msg.Set("sent_at", at)
		msg.Set("received_at", at)
		msg.Set("mailbox", from.Address)
		msg.Set("mailbox_domain", ingest.DomainOf(from.Address))
		msg.Set("body_source", "plain")
		msg.Set("forward_attachments", []string{})
		if err := tx.Save(msg); err != nil {
			return err
		}
		if err := ingest.ReplaceAttachments(tx, msg.Id, parsed.Attachments); err != nil {
			return err
		}
		for _, r := range recipients {
			if err := ingest.TouchContact(tx, r, at); err != nil {
				return err
			}
		}
		return threading.Assign(tx, msg)
	})
}

func markSent(app core.App, id, providerID string, now time.Time) error {
	rec, err := app.FindRecordById("messages", id)
	if err != nil {
		return err
	}
	at, err := types.ParseDateTime(now)
	if err != nil {
		return err
	}
	rec.Set("send_status", "sent")
	rec.Set("provider_id", providerID)
	rec.Set("sent_at", at)
	rec.Set("received_at", at)
	return app.Save(rec)
}

func (s *Service) log(messageID, kind, status, providerID, errText, eventID string, detail any) error {
	collection, err := s.App.FindCollectionByNameOrId("send_log")
	if err != nil {
		return err
	}
	return saveLog(s.App, collection, messageID, kind, status, providerID, errText, eventID, detail)
}

func saveLog(app core.App, collection *core.Collection, messageID, kind, status, providerID, errText, eventID string, detail any) error {
	rec := core.NewRecord(collection)
	rec.Set("message", messageID)
	rec.Set("kind", kind)
	rec.Set("status", status)
	rec.Set("provider_id", providerID)
	rec.Set("error", errText)
	rec.Set("event_id", eventID)
	if detail != nil {
		rec.Set("detail", detail)
	}
	return app.Save(rec)
}

func newMessageID(recordID, domain string) string {
	return fmt.Sprintf("roost.%s.%s@%s", recordID, randHex(6), domain)
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func newUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	x := hex.EncodeToString(b)
	return x[0:8] + "-" + x[8:12] + "-" + x[12:16] + "-" + x[16:20] + "-" + x[20:32]
}
