package ingest

import (
	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/types"

	"roost/server/internal/mailparse"
)

// Helpers shared with outbound, which stores sent mail the same way ingest
// stores received mail: a write-once raw EML plus indexed rows.

// WriteRaw writes raw to mailDir/<key> atomically (temp file + rename).
func WriteRaw(mailDir, key string, raw []byte) error {
	return (&Ingester{MailDir: mailDir}).writeRaw(key, raw)
}

// ReplaceAttachments makes the attachment rows of a message match list.
func ReplaceAttachments(tx core.App, messageID string, list []mailparse.Attachment) error {
	return replaceAttachments(tx, messageID, list)
}

// TouchContact upserts an address the owner corresponds with.
func TouchContact(tx core.App, a mailparse.Address, at types.DateTime) error {
	return touchContact(tx, a, at)
}

// DomainOf returns the lower-cased domain of an address.
func DomainOf(address string) string { return domainOf(address) }
