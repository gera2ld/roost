// Package threading groups messages into threads.
//
// Header links decide: a message joins the thread of the closest message it
// references (In-Reply-To, then References from newest to oldest). Messages that
// arrived earlier but reference this one are pulled in too, merging threads when a
// late parent connects them. Only when a reply carries no usable links does the
// normalized subject decide, and then only within a recent window and with a
// shared participant, so unrelated "Re: hello" mails stay apart.
package threading

import (
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/types"
)

// SubjectWindow bounds how old a thread may be for a subject-only match.
const SubjectWindow = 30 * 24 * time.Hour

const maxParticipants = 10

// Register keeps thread counts in sync when messages change outside ingest
// (read/unread, moves, deletes).
func Register(app core.App) {
	refresh := func(e *core.RecordEvent) error {
		for _, id := range []string{e.Record.GetString("thread"), e.Record.Original().GetString("thread")} {
			if id != "" {
				if err := Recompute(e.App, id); err != nil {
					e.App.Logger().Error("recompute thread", "thread", id, "error", err)
				}
			}
		}
		return e.Next()
	}
	app.OnRecordAfterUpdateSuccess("messages").BindFunc(refresh)
	app.OnRecordAfterDeleteSuccess("messages").BindFunc(refresh)
}

// Address mirrors mailparse.Address as stored in the messages JSON fields.
type Address struct {
	Name    string `json:"name,omitempty"`
	Address string `json:"address"`
}

// Assign puts a saved message into a thread (creating, joining or merging as
// needed), saves it, and refreshes the affected thread.
func Assign(tx core.App, msg *core.Record) error {
	refs := linkedIDs(msg)

	var target string
	for _, id := range refs {
		parent, err := findOne(tx, "messages", "message_id = {:id} && thread != '' && id != {:self}",
			map[string]any{"id": id, "self": msg.Id})
		if err != nil {
			return err
		}
		if parent != nil {
			target = parent.GetString("thread")
			break
		}
	}

	// Siblings: messages that reference the same ancestor we have not received
	// (yet). They belong together even before that ancestor arrives.
	if target == "" {
		for _, id := range refs {
			siblings, err := threadsReferencingID(tx, id, msg.Id)
			if err != nil {
				return err
			}
			if len(siblings) > 0 {
				target = siblings[0]
				break
			}
		}
	}

	childThreads, err := threadsReferencingID(tx, msg.GetString("message_id"), msg.Id)
	if err != nil {
		return err
	}
	if target == "" && len(childThreads) > 0 {
		target = childThreads[0]
	}

	if target == "" && len(refs) == 0 {
		if target, err = subjectMatch(tx, msg); err != nil {
			return err
		}
	}

	if target == "" {
		threads, err := tx.FindCollectionByNameOrId("threads")
		if err != nil {
			return err
		}
		thread := core.NewRecord(threads)
		display, key, _ := NormalizeSubject(msg.GetString("subject"))
		thread.Set("subject", display)
		thread.Set("subject_key", key)
		if err := tx.Save(thread); err != nil {
			return fmt.Errorf("create thread: %w", err)
		}
		target = thread.Id
	}

	// A late parent can connect threads that grew separately: fold them in.
	for _, other := range childThreads {
		if other != target {
			if err := merge(tx, other, target); err != nil {
				return err
			}
		}
	}

	previous := msg.Original().GetString("thread")
	msg.Set("thread", target)
	if err := tx.Save(msg); err != nil {
		return fmt.Errorf("save message thread: %w", err)
	}
	if previous != "" && previous != target {
		if err := Recompute(tx, previous); err != nil {
			return err
		}
	}
	return Recompute(tx, target)
}

// linkedIDs lists referenced Message-IDs, closest first: In-Reply-To, then
// References from last to first.
func linkedIDs(msg *core.Record) []string {
	var refs []string
	_ = msg.UnmarshalJSONField("references", &refs)
	var out []string
	add := func(id string) {
		if id != "" && id != msg.GetString("message_id") && !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	add(msg.GetString("in_reply_to"))
	for i := len(refs) - 1; i >= 0; i-- {
		add(refs[i])
	}
	return out
}

// threadsReferencingID returns the threads of messages (other than self) that
// reference the Message-ID id, oldest first.
func threadsReferencingID(tx core.App, id, self string) ([]string, error) {
	if id == "" {
		return nil, nil
	}
	// references is a JSON array of strings; match the quoted id to avoid
	// substring hits on longer ids.
	children, err := tx.FindRecordsByFilter("messages",
		"thread != '' && id != {:self} && (in_reply_to = {:id} || references ~ {:quoted})",
		"received_at", 0, 0,
		map[string]any{"id": id, "quoted": `"` + id + `"`, "self": self})
	if err != nil {
		return nil, err
	}
	var threads []string
	for _, c := range children {
		if t := c.GetString("thread"); !slices.Contains(threads, t) {
			threads = append(threads, t)
		}
	}
	return threads, nil
}

// subjectMatch finds a recent thread with the same normalized subject that shares
// a participant. Only replies/forwards qualify: a fresh "Hello" starts a thread.
func subjectMatch(tx core.App, msg *core.Record) (string, error) {
	_, key, reply := NormalizeSubject(msg.GetString("subject"))
	if !reply || key == "" {
		return "", nil
	}
	at := msg.GetDateTime("received_at").Time()
	if at.IsZero() {
		at = time.Now()
	}
	candidates, err := tx.FindRecordsByFilter("threads",
		"subject_key = {:key} && latest_at >= {:since}", "-latest_at", 5, 0,
		map[string]any{"key": key, "since": at.Add(-SubjectWindow).UTC().Format(types.DefaultDateLayout)})
	if err != nil {
		return "", err
	}
	mine := addressesOf(msg)
	for _, t := range candidates {
		var people []Address
		_ = t.UnmarshalJSONField("participants", &people)
		for _, p := range people {
			if slices.Contains(mine, p.Address) {
				return t.Id, nil
			}
		}
	}
	return "", nil
}

func merge(tx core.App, from, into string) error {
	moved, err := tx.FindRecordsByFilter("messages", "thread = {:t}", "", 0, 0, map[string]any{"t": from})
	if err != nil {
		return err
	}
	for _, m := range moved {
		m.Set("thread", into)
		if err := tx.Save(m); err != nil {
			return err
		}
	}
	return Recompute(tx, from) // now empty: deleted
}

// Recompute refreshes a thread's counts, participants, subject and snippet from
// its messages, deleting it when it has none left.
func Recompute(tx core.App, threadID string) error {
	thread, err := tx.FindRecordById("threads", threadID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	messages, err := tx.FindRecordsByFilter("messages", "thread = {:t}", "received_at", 0, 0,
		map[string]any{"t": threadID})
	if err != nil {
		return err
	}
	if len(messages) == 0 {
		return tx.Delete(thread)
	}

	own, err := ownDomains(tx)
	if err != nil {
		return err
	}
	unread := 0
	var inbound, outbound, starred bool
	var mailboxes []string
	var people []Address
	seen := map[string]bool{}
	addPerson := func(a Address) {
		if a.Address == "" || seen[a.Address] || own[domainOf(a.Address)] || len(people) >= maxParticipants {
			return
		}
		seen[a.Address] = true
		people = append(people, a)
	}
	for _, m := range messages {
		inbound = inbound || m.GetString("direction") == "inbound"
		outbound = outbound || m.GetString("direction") == "outbound"
		starred = starred || m.GetBool("is_starred")
		if mb := m.GetString("mailbox"); mb != "" && !slices.Contains(mailboxes, mb) {
			mailboxes = append(mailboxes, mb)
		}
		if m.GetString("direction") == "inbound" && !m.GetBool("is_read") {
			unread++
		}
		addPerson(Address{Name: m.GetString("from_name"), Address: m.GetString("from_address")})
		for _, field := range []string{"to", "cc"} {
			var list []Address
			_ = m.UnmarshalJSONField(field, &list)
			for _, a := range list {
				addPerson(a)
			}
		}
	}

	first, latest := messages[0], messages[len(messages)-1]
	display, key, _ := NormalizeSubject(first.GetString("subject"))
	thread.Set("subject", display)
	thread.Set("subject_key", key)
	thread.Set("participants", nonNil(people))
	thread.Set("latest_at", latest.GetDateTime("received_at"))
	thread.Set("message_count", len(messages))
	thread.Set("unread_count", unread)
	thread.Set("snippet", Snippet(latest.GetString("text_body")))
	thread.Set("has_inbound", inbound)
	thread.Set("has_outbound", outbound)
	thread.Set("has_starred", starred)
	thread.Set("mailboxes", nonNil(mailboxes))
	return tx.Save(thread)
}

func addressesOf(msg *core.Record) []string {
	out := []string{msg.GetString("from_address")}
	for _, field := range []string{"to", "cc"} {
		var list []Address
		_ = msg.UnmarshalJSONField(field, &list)
		for _, a := range list {
			out = append(out, a.Address)
		}
	}
	return out
}

func ownDomains(tx core.App) (map[string]bool, error) {
	records, err := tx.FindAllRecords("domains")
	if err != nil {
		return nil, err
	}
	own := make(map[string]bool, len(records))
	for _, r := range records {
		own[strings.ToLower(r.GetString("domain"))] = true
	}
	return own, nil
}

func domainOf(address string) string {
	if at := strings.LastIndexByte(address, '@'); at >= 0 {
		return strings.ToLower(address[at+1:])
	}
	return ""
}

func findOne(tx core.App, collection, filter string, params map[string]any) (*core.Record, error) {
	rec, err := tx.FindFirstRecordByFilter(collection, filter, params)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return rec, err
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
