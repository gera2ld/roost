package ingest

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
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

// search runs a query in the JSON form the web client sends, returning thread
// ids sorted (order is tested on its own).
func search(t *testing.T, app core.App, q string) ([]string, error) {
	t.Helper()
	var items []store.Query
	if err := json.Unmarshal([]byte(q), &items); err != nil {
		t.Fatalf("bad test query %s: %v", q, err)
	}
	ids, err := store.SearchThreads(app, items, store.SearchOptions{Limit: 10})
	slices.Sort(ids)
	return ids, err
}

func TestSearch(t *testing.T) {
	h := newHarness(t)
	gbk := h.deliver(fixture(t, "gbk.eml")).GetString("thread")
	plain := h.deliver(fixture(t, "plain-utf8.eml")).GetString("thread")
	html := h.deliver(fixture(t, "html-only.eml")).GetString("thread")
	multi := h.deliver(fixture(t, "multipart-attachments.eml")).GetString("thread")
	all := []string{gbk, plain, html, multi}
	slices.Sort(all)
	sorted := func(ids ...string) []string { slices.Sort(ids); return ids }

	cases := map[string][]string{
		`[{"field":null,"query":"下午三"}]`:                {gbk},   // CJK substring via trigram
		`[{"field":null,"query":"café"}]`:               {plain}, // case-insensitive
		`[{"field":null,"query":"李四"}]`:                 {gbk},   // 2 chars: LIKE fallback on from_name
		`[{"field":null,"query":"zhang@example"}]`:      {plain}, // sender address
		`[{"field":null,"query":"nothing-matches-me"}]`: nil,
		`[{"field":null,"query":"\"; DROP"}]`:           nil, // FTS syntax is neutralized
		`[{"field":null,"query":"100%"}]`:               nil,
		`[{"field":null,"query":""}]`:                   nil, // nothing left: no filter is no result
		`[]`:                                            nil,
		`[{"group":[{"field":null,"query":"caf"},{"field":null,"query":"meeting"}]}]`: {plain},             // groups AND
		`[{"field":null,"query":"meeting"},{"field":null,"query":"digest"}]`:          sorted(plain, html), // top level ORs
		`[{"group":[{"field":null,"query":"caf"},{"field":null,"query":"digest"}]}]`:  nil,
		`[{"group":[{"field":null,"query":"meeting"}],"negate":true}]`:                sorted(gbk, html, multi),
		`[{"field":"subject","query":"meeting"}]`:                                     {plain},
		`[{"field":"SUBJECT","query":"meeting"}]`:                                     {plain}, // field names ignore case
		`[{"field":"subject","query":"zhang"}]`:                                       nil,     // only the subject
		`[{"field":"from","query":"zhang"}]`:                                          {plain},
		`[{"field":"from","query":"李四"}]`:                                             {gbk},
		`[{"field":"body","query":"wins"}]`:                                           {multi},
		`[{"field":"body","query":"meeting"}]`:                                        nil,
		`[{"field":"to","query":"other@roost"}]`:                                      {plain},
		`[{"field":"to","query":"Me"}]`:                                               all, // name or address
		`[{"field":"cc","query":"roost"}]`:                                            nil,
		`[{"field":"mailbox","query":"me@roost"}]`:                                    all,
		`[{"field":"domain","query":"@Roost.test"}]`:                                  all,
		`[{"field":"domain","query":"roost"}]`:                                        nil, // exact, not a substring
		`[{"field":"has","query":"attachment"}]`:                                      {multi},
		`[{"field":"has","query":"attachment","negate":true}]`:                        sorted(gbk, plain, html),
		`[{"field":"filename","query":"evil"}]`:                                       {multi},
		`[{"field":"is","query":"unread"}]`:                                           all,
		`[{"field":"is","query":"Starred"}]`:                                          nil,
		`[{"field":"is","query":"sent"}]`:                                             nil,
		`[{"field":"label","query":"work"}]`:                                          nil,
		`[{"field":"after","query":"2000-01-01"}]`:                                    all,
		`[{"field":"before","query":"2000-01-01"}]`:                                   nil,
		`[{"field":"after","query":"1000y"}]`:                                         all,
	}
	for q, want := range cases {
		got, err := search(t, h.app, q)
		if err != nil {
			t.Errorf("search %s: %v", q, err)
			continue
		}
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("search %s = %v, want %v", q, got, want)
		}
	}

	for _, q := range []string{
		`[{"field":"is","query":"draft"}]`,
		`[{"field":"has","query":"pdf"}]`,
		`[{"field":"after","query":"yesterday"}]`,
	} {
		if _, err := search(t, h.app, q); !store.IsQueryError(err) {
			t.Errorf("search %s: err = %v, want a query error", q, err)
		}
	}

	// Most recent match first.
	if ids, err := store.SearchThreads(h.app, []store.Query{{Query: "example"}}, store.SearchOptions{}); err != nil ||
		len(ids) != 4 || ids[0] != multi {
		t.Errorf("order = %v, %v; want %s first", ids, err, multi)
	}

	// The index follows edits and deletes.
	rec, _ := h.app.FindFirstRecordByData("messages", "thread", plain)
	rec.Set("subject", "Renamed zebra subject at 10:30")
	if err := h.app.Save(rec); err != nil {
		t.Fatal(err)
	}
	if got, _ := search(t, h.app, `[{"field":null,"query":"zebra"}]`); len(got) != 1 {
		t.Errorf("updated subject not indexed: %v", got)
	}
	// An unknown field is text with a colon in it: "10:30" parses as 10 → 30.
	if got, _ := search(t, h.app, `[{"field":"10","query":"30"}]`); len(got) != 1 {
		t.Errorf("unknown field not searched as text: %v", got)
	}
	if err := h.app.Delete(rec); err != nil {
		t.Fatal(err)
	}
	if got, _ := search(t, h.app, `[{"field":null,"query":"zebra"}]`); len(got) != 0 {
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
	if got, _ := search(t, h.app, `[{"field":null,"query":"下午三"}]`); len(got) != 1 {
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
