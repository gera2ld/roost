// Package mailparse turns raw RFC 5322 messages into the fields Roost indexes.
// All input is untrusted: nothing here renders HTML, and filenames are sanitized.
package mailparse

import (
	"bytes"
	"fmt"
	"net/mail"
	"path"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/jhillyerd/enmime/v2"
)

// Address is one parsed mailbox.
type Address struct {
	Name    string `json:"name,omitempty"`
	Address string `json:"address"`
}

// Body sources, matching messages.body_source.
const (
	BodyPlain = "plain" // from a text/plain part
	BodyHTML  = "html"  // down-converted from an HTML-only message
	BodyNone  = "none"
)

// Attachment describes one non-body MIME part. Content is not kept: it is
// re-extracted from the raw message by PartID when downloaded.
type Attachment struct {
	Filename    string
	ContentType string
	Size        int
	PartID      string
	ContentID   string
	Disposition string // "attachment" or "inline"
}

// Message is the indexed view of one raw message.
type Message struct {
	MessageID   string
	InReplyTo   string
	References  []string
	Subject     string
	From        Address
	To          []Address
	Cc          []Address
	ReplyTo     []Address
	Date        time.Time // zero when missing or unparseable
	Text        string
	BodySource  string
	Attachments []Attachment
}

// Parse reads one raw message. It is lenient: malformed parts and headers degrade
// to empty fields rather than failing, because losing a message is worse than
// showing it imperfectly. The raw bytes are kept elsewhere for reparsing.
func Parse(raw []byte) (*Message, error) {
	parser := enmime.NewParser(enmime.SkipMalformedParts(true))
	env, err := parser.ReadEnvelope(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("parse message: %w", err)
	}

	m := &Message{
		MessageID:  firstMsgID(env.GetHeader("Message-ID")),
		InReplyTo:  firstMsgID(env.GetHeader("In-Reply-To")),
		References: msgIDs(env.GetHeader("References")),
		Subject:    strings.TrimSpace(env.GetHeader("Subject")),
		To:         addresses(env, "To"),
		Cc:         addresses(env, "Cc"),
		ReplyTo:    addresses(env, "Reply-To"),
		Text:       env.Text,
	}
	if from := addresses(env, "From"); len(from) > 0 {
		m.From = from[0]
	}
	if d, err := env.Date(); err == nil {
		m.Date = d.UTC()
	}

	switch {
	case strings.TrimSpace(env.Text) == "":
		m.BodySource = BodyNone
	case hasTextPlainBody(env.Root):
		m.BodySource = BodyPlain
	default:
		m.BodySource = BodyHTML
	}

	for _, p := range env.Attachments {
		m.Attachments = append(m.Attachments, attachment(p, "attachment", len(m.Attachments)))
	}
	for _, p := range env.Inlines {
		m.Attachments = append(m.Attachments, attachment(p, "inline", len(m.Attachments)))
	}
	for _, p := range env.OtherParts {
		if strings.HasPrefix(p.ContentType, "multipart/") {
			continue
		}
		m.Attachments = append(m.Attachments, attachment(p, "inline", len(m.Attachments)))
	}
	return m, nil
}

var msgIDPattern = regexp.MustCompile(`<([^<>\s]+)>`)

// msgIDs extracts every <id> from a header, without the angle brackets.
func msgIDs(header string) []string {
	var ids []string
	for _, match := range msgIDPattern.FindAllStringSubmatch(header, -1) {
		ids = append(ids, match[1])
	}
	return ids
}

// firstMsgID returns the first <id>, or the trimmed header when it lacks brackets.
func firstMsgID(header string) string {
	if ids := msgIDs(header); len(ids) > 0 {
		return ids[0]
	}
	return strings.Trim(strings.TrimSpace(header), "<>")
}

func addresses(env *enmime.Envelope, header string) []Address {
	list, err := env.AddressList(header)
	if err != nil {
		// Keep something readable rather than dropping a malformed header.
		if raw := strings.TrimSpace(env.GetHeader(header)); raw != "" {
			return []Address{{Address: raw}}
		}
		return nil
	}
	out := make([]Address, 0, len(list))
	for _, a := range list {
		out = append(out, fromMailAddress(a))
	}
	return out
}

func fromMailAddress(a *mail.Address) Address {
	return Address{Name: strings.TrimSpace(a.Name), Address: strings.ToLower(strings.TrimSpace(a.Address))}
}

func hasTextPlainBody(p *enmime.Part) bool {
	if p == nil {
		return false
	}
	if p.ContentType == "text/plain" && p.Disposition != "attachment" {
		return true
	}
	for c := p.FirstChild; c != nil; c = c.NextSibling {
		if hasTextPlainBody(c) {
			return true
		}
	}
	return false
}

func attachment(p *enmime.Part, disposition string, index int) Attachment {
	if p.Disposition == "attachment" || p.Disposition == "inline" {
		disposition = p.Disposition
	}
	return Attachment{
		Filename:    SanitizeFilename(p.FileName, index),
		ContentType: strings.ToLower(p.ContentType),
		Size:        len(p.Content),
		PartID:      p.PartID,
		ContentID:   strings.Trim(p.ContentID, "<>"),
		Disposition: disposition,
	}
}

const maxFilenameRunes = 200

// SanitizeFilename reduces an untrusted filename to a safe base name: no directory
// parts, no control or path characters, no leading dots, bounded length. An empty
// result becomes attachment-<n>.
func SanitizeFilename(name string, index int) string {
	name = strings.ReplaceAll(name, "\\", "/")
	name = path.Base(strings.TrimSpace(name))
	var b strings.Builder
	for _, r := range name {
		switch {
		case unicode.IsControl(r), r == '/', r == ':', r == '*', r == '?', r == '"', r == '<', r == '>', r == '|':
			b.WriteRune('_')
		default:
			b.WriteRune(r)
		}
	}
	name = strings.TrimLeft(strings.TrimSpace(b.String()), ".")
	if runes := []rune(name); len(runes) > maxFilenameRunes {
		ext := path.Ext(name)
		if len([]rune(ext)) > 20 {
			ext = ""
		}
		name = string(runes[:maxFilenameRunes-len([]rune(ext))]) + ext
	}
	if name == "" {
		return fmt.Sprintf("attachment-%d", index+1)
	}
	return name
}
