package notify

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// PreviewRunes bounds the body preview in an alert.
const PreviewRunes = 200

// Item is one new message, as shown in an alert.
type Item struct {
	FromName    string
	FromAddress string
	Mailbox     string
	Subject     string
	Body        string
	ThreadID    string
}

var htmlEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

// escape makes untrusted text safe for Telegram's HTML parse mode.
func escape(s string) string { return htmlEscaper.Replace(s) }

// Preview is the start of a body: quoted lines dropped, whitespace collapsed,
// cut at PreviewRunes.
func Preview(body string) string {
	var kept []string
	for line := range strings.SplitSeq(body, "\n") {
		if t := strings.TrimSpace(line); t != "" && !strings.HasPrefix(t, ">") {
			kept = append(kept, t)
		}
	}
	return truncate(strings.Join(strings.Fields(strings.Join(kept, " ")), " "), PreviewRunes)
}

func sender(it Item) string {
	if it.FromName != "" {
		return truncate(it.FromName, 80)
	}
	if it.FromAddress != "" {
		return it.FromAddress
	}
	return "(unknown sender)"
}

// maxSubjectRunes keeps a hostile subject from blowing Telegram's 4096-char limit.
const maxSubjectRunes = 160

func subject(it Item) string {
	if it.Subject == "" {
		return "(no subject)"
	}
	return truncate(it.Subject, maxSubjectRunes)
}

func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return strings.TrimSpace(string([]rune(s)[:n])) + "…"
}

// link builds a deep link into the PWA, or "" when no public URL is set.
func link(publicURL, path string) string {
	base := strings.TrimRight(publicURL, "/")
	if !strings.HasPrefix(base, "https://") && !strings.HasPrefix(base, "http://") {
		return ""
	}
	return base + path
}

// FormatOne renders an alert for a single message.
func FormatOne(it Item, publicURL string) (string, *Button) {
	var b strings.Builder
	fmt.Fprintf(&b, "<b>%s</b>", escape(sender(it)))
	if it.FromName != "" && it.FromAddress != "" {
		fmt.Fprintf(&b, " &lt;%s&gt;", escape(it.FromAddress))
	}
	if it.Mailbox != "" {
		fmt.Fprintf(&b, "\nto %s", escape(it.Mailbox))
	}
	fmt.Fprintf(&b, "\n<b>%s</b>", escape(subject(it)))
	if p := Preview(it.Body); p != "" {
		fmt.Fprintf(&b, "\n%s", escape(p))
	}
	var button *Button
	if it.ThreadID != "" {
		if u := link(publicURL, "/inbox/"+it.ThreadID); u != "" {
			button = &Button{Text: "Open", URL: u}
		}
	}
	return b.String(), button
}

// maxListed bounds how many messages a batch alert names.
const maxListed = 10

// FormatBatch renders one alert for a burst of messages.
func FormatBatch(items []Item, publicURL string) (string, *Button) {
	var b strings.Builder
	fmt.Fprintf(&b, "<b>%d new messages</b>", len(items))
	for i, it := range items {
		if i == maxListed {
			fmt.Fprintf(&b, "\n…and %d more", len(items)-maxListed)
			break
		}
		fmt.Fprintf(&b, "\n• <b>%s</b>: %s", escape(sender(it)), escape(subject(it)))
	}
	var button *Button
	if u := link(publicURL, "/inbox"); u != "" {
		button = &Button{Text: "Open inbox", URL: u}
	}
	return b.String(), button
}

// FormatDelivery renders an alert for a sent message that bounced or drew a
// spam complaint. Recipients and reason come from the provider; both are escaped.
func FormatDelivery(status, recipients, subj, reason, threadID, publicURL string) (string, *Button) {
	var b strings.Builder
	fmt.Fprintf(&b, "<b>Sent mail %s</b>", escape(status))
	if recipients != "" {
		fmt.Fprintf(&b, "\nto %s", escape(truncate(recipients, maxSubjectRunes)))
	}
	fmt.Fprintf(&b, "\n<b>%s</b>", escape(subject(Item{Subject: subj})))
	if reason != "" {
		fmt.Fprintf(&b, "\n%s", escape(truncate(reason, PreviewRunes)))
	}
	var button *Button
	if threadID != "" {
		if u := link(publicURL, "/sent/"+threadID); u != "" {
			button = &Button{Text: "Open", URL: u}
		}
	}
	return b.String(), button
}

// FormatOps renders an operations alert (or its all-clear when resolved).
func FormatOps(title, detail string, resolved bool) string {
	if resolved {
		return "✅ <b>Resolved:</b> " + escape(title)
	}
	text := "⚠️ <b>" + escape(title) + "</b>"
	if detail != "" {
		text += "\n" + escape(detail)
	}
	return text
}
