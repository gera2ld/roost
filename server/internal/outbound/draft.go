package outbound

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"ganigeorgiev.com/pocketbase/pocketbase/core"

	"roost/server/internal/ingest"
	"roost/server/internal/threading"
)

// Mode is how a draft relates to the message it starts from.
type Mode string

const (
	New      Mode = "new"
	Reply    Mode = "reply"
	ReplyAll Mode = "reply_all"
	Forward  Mode = "forward"
)

const maxReferences = 20

// NewDraft creates an outbound draft, prefilled from src for replies and
// forwards. loc decides how the quoted date reads.
func NewDraft(app core.App, mode Mode, src *core.Record, loc *time.Location) (*core.Record, error) {
	collection, err := app.FindCollectionByNameOrId("messages")
	if err != nil {
		return nil, err
	}
	d := core.NewRecord(collection)
	d.Set("direction", "outbound")
	d.Set("send_status", "draft")
	d.Set("is_read", true)
	for _, f := range []string{"to", "cc", "bcc", "reply_to", "references", "forward_attachments"} {
		d.Set(f, []any{})
	}

	if mode == New {
		if from := DefaultFrom(app); from != "" {
			applyIdentity(app, d, from, "")
		}
	} else {
		if src == nil {
			return nil, fmt.Errorf("%s needs a source message", mode)
		}
		if err := prefill(app, d, mode, src, loc); err != nil {
			return nil, err
		}
	}
	if err := app.Save(d); err != nil {
		return nil, err
	}
	return d, nil
}

func prefill(app core.App, d *core.Record, mode Mode, src *core.Record, loc *time.Location) error {
	received := fromFor(src)
	from := received
	if !CanSend(app, ingest.DomainOf(from)) {
		// Received on a receive-only domain: answer from the default identity.
		if def := DefaultFrom(app); def != "" {
			from = def
		}
	}

	display, _, _ := threading.NormalizeSubject(src.GetString("subject"))
	refs := referencesFor(src)
	switch mode {
	case Reply, ReplyAll:
		to, cc := Recipients(src, mode == ReplyAll, received, from)
		d.Set("to", to)
		d.Set("cc", cc)
		d.Set("subject", "Re: "+display)
		d.Set("in_reply_to", src.GetString("message_id"))
		d.Set("references", refs)
		applyIdentity(app, d, from, Quote(src, loc))
	case Forward:
		d.Set("subject", "Fwd: "+display)
		d.Set("references", refs) // keeps the forward in the conversation
		applyIdentity(app, d, from, ForwardHeader(src, loc)+src.GetString("text_body"))
		atts, err := app.FindRecordsByFilter("attachments", "message = {:m} && disposition != 'inline'",
			"filename", 0, 0, map[string]any{"m": src.Id})
		if err != nil {
			return err
		}
		ids := []string{}
		for _, a := range atts {
			ids = append(ids, a.Id)
		}
		d.Set("forward_attachments", ids)
	default:
		return fmt.Errorf("unknown mode %q", mode)
	}
	return nil
}

// fromFor picks the sending address: the address the mail was received at, or
// the address a sent message went out from.
func fromFor(src *core.Record) string {
	if src.GetString("direction") == "outbound" {
		return src.GetString("from_address")
	}
	return strings.ToLower(src.GetString("mailbox"))
}

// lastNameUsed reuses the display name of the newest message sent from address.
func lastNameUsed(app core.App, address string) string {
	if address == "" {
		return ""
	}
	recs, err := app.FindRecordsByFilter("messages",
		"direction = 'outbound' && send_status = 'sent' && from_address = {:a} && from_name != ''",
		"-received_at", 1, 0, map[string]any{"a": address})
	if err != nil || len(recs) == 0 {
		return ""
	}
	return recs[0].GetString("from_name")
}

func referencesFor(src *core.Record) []string {
	var refs []string
	_ = src.UnmarshalJSONField("references", &refs)
	if id := src.GetString("message_id"); id != "" && !slices.Contains(refs, id) {
		refs = append(refs, id)
	}
	if len(refs) > maxReferences { // keep the root and the newest
		refs = append(refs[:1], refs[len(refs)-maxReferences+1:]...)
	}
	if refs == nil {
		refs = []string{}
	}
	return refs
}

// Recipients computes To and Cc for a reply. A reply goes to Reply-To (else
// From); replying to your own sent message goes to its original recipients.
// Reply-all adds the other To/Cc addresses. The owner's addresses (self) never appear.
func Recipients(src *core.Record, all bool, self ...string) (to, cc []Address) {
	field := func(name string) []Address {
		var list []Address
		_ = src.UnmarshalJSONField(name, &list)
		return list
	}
	seen := map[string]bool{}
	for _, s := range self {
		seen[strings.ToLower(s)] = true
	}
	add := func(dst *[]Address, list ...Address) {
		for _, a := range list {
			key := strings.ToLower(a.Address)
			if key == "" || seen[key] {
				continue
			}
			seen[key] = true
			*dst = append(*dst, a)
		}
	}
	to, cc = []Address{}, []Address{}
	if src.GetString("direction") == "outbound" {
		add(&to, field("to")...)
		if all {
			add(&cc, field("cc")...)
		}
		return to, cc
	}
	if rt := field("reply_to"); len(rt) > 0 {
		add(&to, rt...)
	} else {
		add(&to, Address{Name: src.GetString("from_name"), Address: src.GetString("from_address")})
	}
	if all {
		add(&to, field("to")...)
		add(&cc, field("cc")...)
	}
	return to, cc
}

func when(src *core.Record, loc *time.Location) string {
	t := src.GetDateTime("sent_at").Time()
	if t.IsZero() {
		t = src.GetDateTime("received_at").Time()
	}
	if loc == nil {
		loc = time.UTC
	}
	return t.In(loc).Format("Mon, Jan 2, 2006 at 3:04 PM")
}

// Quote renders the attribution line and the quoted original body.
func Quote(src *core.Record, loc *time.Location) string {
	var b strings.Builder
	fmt.Fprintf(&b, "On %s, %s wrote:\n", when(src, loc), displaySender(src))
	body := strings.TrimRight(src.GetString("text_body"), "\n")
	for line := range strings.SplitSeq(body, "\n") {
		if strings.HasPrefix(line, ">") {
			b.WriteString(">" + line + "\n")
		} else {
			b.WriteString("> " + line + "\n")
		}
	}
	return b.String()
}

func displaySender(src *core.Record) string {
	if name := src.GetString("from_name"); name != "" {
		return fmt.Sprintf("%s <%s>", name, src.GetString("from_address"))
	}
	return src.GetString("from_address")
}

// ForwardHeader is the block that introduces a forwarded message.
func ForwardHeader(src *core.Record, loc *time.Location) string {
	var to []Address
	_ = src.UnmarshalJSONField("to", &to)
	names := make([]string, 0, len(to))
	for _, a := range to {
		if a.Name != "" {
			names = append(names, fmt.Sprintf("%s <%s>", a.Name, a.Address))
		} else {
			names = append(names, a.Address)
		}
	}
	return fmt.Sprintf("---------- Forwarded message ----------\nFrom: %s\nDate: %s\nSubject: %s\nTo: %s\n\n",
		displaySender(src), when(src, loc), src.GetString("subject"), strings.Join(names, ", "))
}
