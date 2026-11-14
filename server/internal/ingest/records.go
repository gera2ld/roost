package ingest

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/types"

	"roost/server/internal/mailparse"
)

// applyParsed copies everything derived from the raw message onto rec. Shared by
// ingest and reparse, so a parser fix reaches old messages too.
func applyParsed(rec *core.Record, msg *mailparse.Message) {
	rec.Set("message_id", msg.MessageID)
	rec.Set("in_reply_to", msg.InReplyTo)
	rec.Set("references", nonNil(msg.References))
	rec.Set("subject", msg.Subject)
	rec.Set("from_address", msg.From.Address)
	rec.Set("from_name", msg.From.Name)
	rec.Set("to", nonNil(msg.To))
	rec.Set("cc", nonNil(msg.Cc))
	rec.Set("reply_to", nonNil(msg.ReplyTo))
	if msg.Date.IsZero() {
		rec.Set("sent_at", "")
	} else {
		rec.Set("sent_at", msg.Date)
	}
	rec.Set("text_body", msg.Text)
	rec.Set("body_source", msg.BodySource)
}

// replaceAttachments makes the attachment rows of a message match the parse.
func replaceAttachments(tx core.App, messageID string, list []mailparse.Attachment) error {
	old, err := tx.FindRecordsByFilter("attachments", "message = {:m}", "", 0, 0, map[string]any{"m": messageID})
	if err != nil {
		return err
	}
	for _, r := range old {
		if err := tx.Delete(r); err != nil {
			return err
		}
	}
	collection, err := tx.FindCollectionByNameOrId("attachments")
	if err != nil {
		return err
	}
	for _, a := range list {
		ar := core.NewRecord(collection)
		ar.Set("message", messageID)
		ar.Set("filename", a.Filename)
		ar.Set("content_type", a.ContentType)
		ar.Set("size", a.Size)
		ar.Set("part_id", a.PartID)
		ar.Set("content_id", a.ContentID)
		ar.Set("disposition", a.Disposition)
		if err := tx.Save(ar); err != nil {
			return fmt.Errorf("save attachment: %w", err)
		}
	}
	return nil
}

// touchDomain records a domain Roost receives mail for.
func touchDomain(tx core.App, domain string) error {
	if domain == "" {
		return nil
	}
	_, err := tx.FindFirstRecordByData("domains", "domain", domain)
	if err == nil || !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	collection, err := tx.FindCollectionByNameOrId("domains")
	if err != nil {
		return err
	}
	rec := core.NewRecord(collection)
	rec.Set("domain", domain)
	return tx.Save(rec)
}

// touchContact upserts the sender of an inbound message.
func touchContact(tx core.App, from mailparse.Address, at types.DateTime) error {
	address := strings.ToLower(from.Address)
	if !strings.Contains(address, "@") {
		return nil // malformed From: nothing worth remembering
	}
	rec, err := tx.FindFirstRecordByData("contacts", "address", address)
	if errors.Is(err, sql.ErrNoRows) {
		collection, cerr := tx.FindCollectionByNameOrId("contacts")
		if cerr != nil {
			return cerr
		}
		rec, err = core.NewRecord(collection), nil
		rec.Set("address", address)
	}
	if err != nil {
		return err
	}
	if from.Name != "" {
		rec.Set("name", from.Name)
	}
	if at.After(rec.GetDateTime("last_seen_at")) {
		rec.Set("last_seen_at", at)
	}
	rec.Set("message_count", rec.GetInt("message_count")+1)
	return tx.Save(rec)
}

func domainOf(address string) string {
	if at := strings.LastIndexByte(address, '@'); at >= 0 {
		return strings.ToLower(address[at+1:])
	}
	return ""
}

// reviveThread brings a thread back to the inbox when new mail arrives in it, so
// a reply to an archived or trashed conversation is never missed.
func reviveThread(tx core.App, threadID string) error {
	thread, err := tx.FindRecordById("threads", threadID)
	if err != nil {
		return err
	}
	if !thread.GetBool("is_archived") && thread.GetString("trashed_at") == "" {
		return nil
	}
	thread.Set("is_archived", false)
	thread.Set("trashed_at", "")
	return tx.Save(thread)
}
