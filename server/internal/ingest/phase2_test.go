package ingest

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"ganigeorgiev.com/pocketbase/pocketbase/core"

	"roost/server/internal/spool"
	"roost/server/internal/store"
	"roost/server/internal/threading"
)

// eml builds a minimal message. refs may be empty.
func eml(id, from, subject, inReplyTo string, refs ...string) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\nTo: me@roost.test\r\nSubject: %s\r\nDate: Fri, 13 Nov 2026 09:00:00 +0000\r\n", from, subject)
	if id != "" {
		fmt.Fprintf(&b, "Message-ID: <%s>\r\n", id)
	}
	if inReplyTo != "" {
		fmt.Fprintf(&b, "In-Reply-To: <%s>\r\n", inReplyTo)
	}
	if len(refs) > 0 {
		fmt.Fprintf(&b, "References: <%s>\r\n", strings.Join(refs, "> <"))
	}
	fmt.Fprintf(&b, "\r\nBody of %s about %s.\r\n", id, subject)
	return []byte(b.String())
}

// keyAt makes a valid spool key; n orders arrival.
func keyAt(n int) string {
	return fmt.Sprintf("2026/11/13/10%04d-00000000-0000-4000-8000-%012d.eml", n, n)
}

type harness struct {
	t   *testing.T
	in  *Ingester
	mem *spool.Memory
	app core.App
	n   int
}

func newHarness(t *testing.T) *harness {
	in, mem, app := setup(t)
	return &harness{t: t, in: in, mem: mem, app: app}
}

func (h *harness) deliver(raw []byte) *core.Record {
	h.t.Helper()
	h.n++
	key := keyAt(h.n)
	h.mem.Put(key, raw, "sender@example.com", "me@roost.test")
	if out, err := h.in.Ingest(context.Background(), key); err != nil || out != Stored {
		h.t.Fatalf("ingest %d = %v, %v", h.n, out, err)
	}
	rec, err := h.app.FindFirstRecordByData("messages", "r2_key", key)
	if err != nil {
		h.t.Fatal(err)
	}
	return rec
}

func (h *harness) thread(m *core.Record) string {
	h.t.Helper()
	fresh, err := h.app.FindRecordById("messages", m.Id)
	if err != nil {
		h.t.Fatal(err)
	}
	return fresh.GetString("thread")
}

func (h *harness) threadCount() int {
	h.t.Helper()
	n, err := h.app.CountRecords("threads")
	if err != nil {
		h.t.Fatal(err)
	}
	return int(n)
}

func TestReplyJoinsParentThread(t *testing.T) {
	h := newHarness(t)
	root := h.deliver(eml("root@x", "Zhang <zhang@example.com>", "Café meeting", ""))
	reply := h.deliver(eml("reply@x", "Li <li@example.com>", "Re: Café meeting", "root@x", "root@x"))

	if h.thread(root) == "" || h.thread(root) != h.thread(reply) {
		t.Fatal("reply not in the parent's thread")
	}
	thread, _ := h.app.FindRecordById("threads", h.thread(root))
	if thread.GetString("subject") != "Café meeting" || thread.GetInt("message_count") != 2 ||
		thread.GetInt("unread_count") != 2 {
		t.Errorf("thread = %s / %d msgs / %d unread", thread.GetString("subject"),
			thread.GetInt("message_count"), thread.GetInt("unread_count"))
	}
	var people []threading.Address
	_ = thread.UnmarshalJSONField("participants", &people)
	for _, p := range people {
		if strings.HasSuffix(p.Address, "@roost.test") {
			t.Errorf("own address %q listed as participant", p.Address)
		}
	}
	if len(people) != 2 {
		t.Errorf("participants = %+v", people)
	}
	if !strings.Contains(thread.GetString("snippet"), "Body of reply@x") {
		t.Errorf("snippet = %q", thread.GetString("snippet"))
	}
}

func TestOutOfOrderReplyIsPulledIn(t *testing.T) {
	h := newHarness(t)
	reply := h.deliver(eml("reply@x", "li@example.com", "Re: Plan", "root@x", "root@x"))
	root := h.deliver(eml("root@x", "zhang@example.com", "Plan", ""))
	if h.thread(root) != h.thread(reply) || h.threadCount() != 1 {
		t.Fatalf("threads = %d; root %s, reply %s", h.threadCount(), h.thread(root), h.thread(reply))
	}
}

func TestSiblingsGroupBeforeRootAndMergeAfter(t *testing.T) {
	h := newHarness(t)
	a := h.deliver(eml("a@x", "a@example.com", "Re: Trip", "root@x", "root@x"))
	b := h.deliver(eml("b@x", "b@example.com", "Re: Trip", "root@x", "root@x"))
	if h.thread(a) != h.thread(b) {
		t.Fatal("siblings sharing an unseen parent not grouped")
	}
	// A deeper reply reaching root only via References, then the root itself.
	c := h.deliver(eml("c@x", "c@example.com", "Re: Trip", "b@x", "root@x", "b@x"))
	root := h.deliver(eml("root@x", "me@roost.test", "Trip", ""))
	for _, m := range []*core.Record{a, b, c} {
		if h.thread(m) != h.thread(root) {
			t.Fatal("not all messages ended in the root's thread")
		}
	}
	if h.threadCount() != 1 {
		t.Errorf("threads = %d, want 1", h.threadCount())
	}
}

func TestLateParentMergesSeparateThreads(t *testing.T) {
	h := newHarness(t)
	// Two branches that only connect through a message we receive last.
	x := h.deliver(eml("x@x", "x@example.com", "Re: Budget", "mid@x"))
	y := h.deliver(eml("y@x", "y@example.com", "Re: Budget", "other@x"))
	if h.thread(x) == h.thread(y) {
		t.Fatal("unrelated branches grouped early")
	}
	mid := h.deliver(eml("mid@x", "m@example.com", "Re: Budget", "other@x", "other@x"))
	if h.thread(mid) != h.thread(x) || h.thread(mid) != h.thread(y) {
		t.Fatal("late parent did not merge the branches")
	}
	if h.threadCount() != 1 {
		t.Errorf("threads = %d after merge, want 1", h.threadCount())
	}
}

func TestSubjectFallbackIsConservative(t *testing.T) {
	h := newHarness(t)
	first := h.deliver(eml("s1@x", "pat@example.com", "Hello there", ""))
	// A reply with no link headers from the same person: joins by subject.
	sameSender := h.deliver(eml("s2@x", "pat@example.com", "Re: Hello there", ""))
	// Same subject from a stranger: stays apart.
	stranger := h.deliver(eml("s3@x", "sam@other.example", "Re: Hello there", ""))
	// A fresh subject without a reply prefix: new thread even from the same person.
	fresh := h.deliver(eml("s4@x", "pat@example.com", "Hello there", ""))

	if h.thread(sameSender) != h.thread(first) {
		t.Error("reply without headers did not join by subject")
	}
	if h.thread(stranger) == h.thread(first) {
		t.Error("stranger's reply joined by subject")
	}
	if h.thread(fresh) == h.thread(first) {
		t.Error("non-reply joined by subject")
	}
}

func TestReadStateUpdatesThreadCounts(t *testing.T) {
	h := newHarness(t)
	threading.Register(h.app)
	m := h.deliver(eml("r1@x", "pat@example.com", "Status", ""))
	h.deliver(eml("r2@x", "pat@example.com", "Re: Status", "r1@x", "r1@x"))

	rec, _ := h.app.FindRecordById("messages", m.Id)
	rec.Set("is_read", true)
	if err := h.app.Save(rec); err != nil {
		t.Fatal(err)
	}
	thread, _ := h.app.FindRecordById("threads", rec.GetString("thread"))
	if thread.GetInt("unread_count") != 1 {
		t.Errorf("unread = %d, want 1", thread.GetInt("unread_count"))
	}

	if err := h.app.Delete(rec); err != nil {
		t.Fatal(err)
	}
	thread, _ = h.app.FindRecordById("threads", thread.Id)
	if thread.GetInt("message_count") != 1 {
		t.Errorf("count after delete = %d, want 1", thread.GetInt("message_count"))
	}
}

func TestDomainsAndContacts(t *testing.T) {
	h := newHarness(t)
	h.deliver(eml("c1@x", "Pat Lee <Pat@Example.com>", "One", ""))
	h.deliver(eml("c2@x", "Pat <pat@example.com>", "Two", ""))

	m, _ := h.app.FindFirstRecordByData("messages", "message_id", "c1@x")
	if m.GetString("mailbox") != "me@roost.test" || m.GetString("mailbox_domain") != "roost.test" {
		t.Errorf("mailbox = %q / %q", m.GetString("mailbox"), m.GetString("mailbox_domain"))
	}
	if _, err := h.app.FindFirstRecordByData("domains", "domain", "roost.test"); err != nil {
		t.Errorf("domain not recorded: %v", err)
	}
	c, err := h.app.FindFirstRecordByData("contacts", "address", "pat@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if c.GetInt("message_count") != 2 || c.GetString("name") != "Pat" {
		t.Errorf("contact = %q, %d", c.GetString("name"), c.GetInt("message_count"))
	}
}

func TestSearch(t *testing.T) {
	h := newHarness(t)
	gbk := h.deliver(fixture(t, "gbk.eml"))
	plain := h.deliver(fixture(t, "plain-utf8.eml"))
	h.deliver(fixture(t, "html-only.eml"))

	cases := map[string][]string{
		"下午三":                {gbk.Id},   // CJK substring via trigram
		"café":               {plain.Id}, // case-insensitive
		"caf meeting":        {plain.Id}, // multiple terms are ANDed
		"李四":                 {gbk.Id},   // 2 chars: LIKE fallback on from_name
		"zhang@example":      {plain.Id}, // sender address
		"nothing-matches-me": nil,
		`"; DROP`:            nil, // FTS syntax is neutralized
		"100%":               nil,
	}
	for q, want := range cases {
		got, err := store.Search(h.app, q, 10)
		if err != nil {
			t.Errorf("Search(%q): %v", q, err)
			continue
		}
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("Search(%q) = %v, want %v", q, got, want)
		}
	}

	// The index follows edits and deletes.
	rec, _ := h.app.FindRecordById("messages", plain.Id)
	rec.Set("subject", "Renamed zebra subject")
	if err := h.app.Save(rec); err != nil {
		t.Fatal(err)
	}
	if got, _ := store.Search(h.app, "zebra", 10); len(got) != 1 {
		t.Errorf("updated subject not indexed: %v", got)
	}
	if err := h.app.Delete(rec); err != nil {
		t.Fatal(err)
	}
	if got, _ := store.Search(h.app, "zebra", 10); len(got) != 0 {
		t.Errorf("deleted message still found: %v", got)
	}
}

func TestSyncIsIdempotentAndKeepsIndex(t *testing.T) {
	h := newHarness(t)
	h.deliver(fixture(t, "gbk.eml"))
	for i := 0; i < 2; i++ {
		if err := store.Sync(h.app); err != nil {
			t.Fatal(err)
		}
	}
	if got, _ := store.Search(h.app, "下午三", 10); len(got) != 1 {
		t.Errorf("index lost after re-sync: %v", got)
	}
}

func TestReparseRestoresParsedFieldsAndKeepsUserState(t *testing.T) {
	h := newHarness(t)
	m := h.deliver(fixture(t, "multipart-attachments.eml"))

	rec, _ := h.app.FindRecordById("messages", m.Id)
	rec.Set("subject", "stale")
	rec.Set("text_body", "stale")
	rec.Set("is_read", true)
	rec.Set("is_starred", true)
	if err := h.app.Save(rec); err != nil {
		t.Fatal(err)
	}
	old, _ := h.app.FindRecordsByFilter("attachments", "message = {:m}", "", 0, 0, map[string]any{"m": m.Id})
	if err := h.app.Delete(old[0]); err != nil {
		t.Fatal(err)
	}

	res, err := Reparse(context.Background(), h.app, h.in.MailDir)
	if err != nil || res.Updated != 1 {
		t.Fatalf("reparse = %+v, %v", res, err)
	}
	rec, _ = h.app.FindRecordById("messages", m.Id)
	if rec.GetString("subject") != "Report attached" || !strings.Contains(rec.GetString("text_body"), "Plain body wins") {
		t.Errorf("fields not restored: %q", rec.GetString("subject"))
	}
	if !rec.GetBool("is_read") || !rec.GetBool("is_starred") || rec.GetString("thread") != m.GetString("thread") {
		t.Error("user state or thread lost")
	}
	if n := count(t, h.app, "attachments", ""); n != 3 {
		t.Errorf("attachments = %d, want 3", n)
	}
}
