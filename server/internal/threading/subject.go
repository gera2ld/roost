package threading

import (
	"regexp"
	"strings"
)

// replyPrefix matches one reply/forward marker, in the languages Roost's mail
// actually uses: "Re:", "Fwd:", "RE[2]:", "AW:", "回复：", "转发:", "転送:" ...
var replyPrefix = regexp.MustCompile(
	`(?i)^\s*(re|fw|fwd|aw|sv|vs|antw|wg|tr|rif|回复|答复|回覆|转发|轉寄|転送)\s*(\[\d+\]|\(\d+\))?\s*[:：]\s*`)

var spaces = regexp.MustCompile(`\s+`)

// NormalizeSubject strips leading reply/forward markers.
// display keeps the original case for showing; key is lowercased for matching;
// reply reports whether any marker was stripped.
func NormalizeSubject(subject string) (display, key string, reply bool) {
	s := strings.TrimSpace(subject)
	for {
		loc := replyPrefix.FindStringIndex(s)
		if loc == nil {
			break
		}
		s = s[loc[1]:]
		reply = true
	}
	display = spaces.ReplaceAllString(strings.TrimSpace(s), " ")
	return display, strings.ToLower(display), reply
}

const snippetRunes = 200

// Snippet is a one-line preview of a body: quoted lines dropped, whitespace
// collapsed, cut to a fixed length.
func Snippet(body string) string {
	var kept []string
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), ">") {
			continue
		}
		kept = append(kept, line)
	}
	s := spaces.ReplaceAllString(strings.TrimSpace(strings.Join(kept, " ")), " ")
	if r := []rune(s); len(r) > snippetRunes {
		s = strings.TrimSpace(string(r[:snippetRunes])) + "…"
	}
	return s
}
