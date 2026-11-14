package threading

import "testing"

func TestNormalizeSubject(t *testing.T) {
	cases := []struct {
		in, display string
		reply       bool
	}{
		{"Hello there", "Hello there", false},
		{"Re: Hello there", "Hello there", true},
		{"RE[2]: Fwd:  Re:Hello   there ", "Hello there", true},
		{"AW: Termin", "Termin", true},
		{"回复：会议通知", "会议通知", true},
		{"转发: 报告", "报告", true},
		{"Re:", "", true},
		{"Regarding: plans", "Regarding: plans", false},
	}
	for _, c := range cases {
		display, key, reply := NormalizeSubject(c.in)
		if display != c.display || reply != c.reply {
			t.Errorf("NormalizeSubject(%q) = %q, %v; want %q, %v", c.in, display, reply, c.display, c.reply)
		}
		if key != toLower(display) {
			t.Errorf("key %q not lowercased display", key)
		}
	}
}

func toLower(s string) string {
	b := []rune(s)
	for i, r := range b {
		if r >= 'A' && r <= 'Z' {
			b[i] = r + 32
		}
	}
	return string(b)
}

func TestSnippet(t *testing.T) {
	body := "Thanks!\n\nSee you then.\n> On Fri, Zhang wrote:\n> old quoted text\n"
	if got := Snippet(body); got != "Thanks! See you then." {
		t.Errorf("Snippet = %q", got)
	}
}
