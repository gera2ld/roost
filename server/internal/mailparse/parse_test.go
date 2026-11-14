package mailparse

import (
	"os"
	"slices"
	"strings"
	"testing"
)

func parseFixture(t *testing.T, name string) *Message {
	t.Helper()
	raw, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	m, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestCharsets(t *testing.T) {
	cases := []struct {
		file, subject, fromName, bodyHas string
	}{
		{"plain-utf8.eml", "Café meeting", "张三", "你好"},
		{"gbk.eml", "会议通知", "李四", "明天下午三点开会"},
		{"iso2022jp.eml", "お知らせ", "山田", "こんにちは、世界"},
	}
	for _, c := range cases {
		t.Run(c.file, func(t *testing.T) {
			m := parseFixture(t, c.file)
			if m.Subject != c.subject {
				t.Errorf("subject = %q, want %q", m.Subject, c.subject)
			}
			if m.From.Name != c.fromName {
				t.Errorf("from name = %q, want %q", m.From.Name, c.fromName)
			}
			if !strings.Contains(m.Text, c.bodyHas) {
				t.Errorf("body %q missing %q", m.Text, c.bodyHas)
			}
			if m.BodySource != BodyPlain {
				t.Errorf("body source = %q", m.BodySource)
			}
		})
	}
}

func TestHeaders(t *testing.T) {
	m := parseFixture(t, "plain-utf8.eml")
	if m.MessageID != "plain-1@example.com" {
		t.Errorf("message id = %q", m.MessageID)
	}
	if m.From.Address != "zhang@example.com" {
		t.Errorf("from address not normalized: %q", m.From.Address)
	}
	if len(m.To) != 2 || m.To[0].Name != "Me" || m.To[1].Address != "other@roost.test" {
		t.Errorf("to = %+v", m.To)
	}
	if m.Date.IsZero() || m.Date.Hour() != 1 || m.Date.Location().String() != "UTC" {
		t.Errorf("date = %v, want 01:30 UTC", m.Date)
	}
}

func TestReplyHeaders(t *testing.T) {
	m := parseFixture(t, "reply.eml")
	if m.InReplyTo != "plain-1@example.com" {
		t.Errorf("in-reply-to = %q", m.InReplyTo)
	}
	if !slices.Equal(m.References, []string{"root-0@example.com", "plain-1@example.com"}) {
		t.Errorf("references = %v", m.References)
	}
}

func TestHTMLOnlyBecomesText(t *testing.T) {
	m := parseFixture(t, "html-only.eml")
	if m.BodySource != BodyHTML {
		t.Fatalf("body source = %q, want html", m.BodySource)
	}
	if !strings.Contains(m.Text, "Top story") {
		t.Errorf("text missing content: %q", m.Text)
	}
	for _, bad := range []string{"<script", "alert(", "<img", "color:red"} {
		if strings.Contains(m.Text, bad) {
			t.Errorf("text kept %q: %q", bad, m.Text)
		}
	}
}

func TestAttachments(t *testing.T) {
	m := parseFixture(t, "multipart-attachments.eml")
	if m.BodySource != BodyPlain || !strings.Contains(m.Text, "Plain body wins.") {
		t.Fatalf("body = %q (%s)", m.Text, m.BodySource)
	}
	byName := map[string]Attachment{}
	for _, a := range m.Attachments {
		byName[a.Filename] = a
	}
	evil, ok := byName["evil.sh"]
	if !ok || evil.Disposition != "attachment" || evil.Size == 0 || evil.PartID == "" {
		t.Errorf("path traversal filename not reduced to base name: %+v", m.Attachments)
	}
	if _, ok := byName["报告.pdf"]; !ok {
		t.Errorf("RFC 2231 filename not decoded: %+v", m.Attachments)
	}
	var inline *Attachment
	for i := range m.Attachments {
		if m.Attachments[i].ContentID == "logo@x" {
			inline = &m.Attachments[i]
		}
	}
	if inline == nil || inline.Disposition != "inline" || inline.ContentType != "image/png" {
		t.Errorf("inline image missing: %+v", m.Attachments)
	}
}

func TestMissingMessageIDAndBadDate(t *testing.T) {
	m := parseFixture(t, "no-message-id.eml")
	if m.MessageID != "" {
		t.Errorf("message id = %q, want empty", m.MessageID)
	}
	if !m.Date.IsZero() {
		t.Errorf("date = %v, want zero", m.Date)
	}
	if !strings.Contains(m.Text, "Body without headers") {
		t.Errorf("text = %q", m.Text)
	}
	if m.From.Address != "broken sender" {
		t.Errorf("malformed from should be kept raw, got %+v", m.From)
	}
}

func TestSanitizeFilename(t *testing.T) {
	cases := map[string]string{
		"report.pdf":                      "report.pdf",
		"../../etc/passwd":                "passwd",
		`C:\Users\x\evil.exe`:             "evil.exe",
		".hidden":                         "hidden",
		"a\x00b\nc.txt":                   "a_b_c.txt",
		"what?.txt":                       "what_.txt",
		"":                                "attachment-3",
		"..":                              "attachment-3",
		strings.Repeat("x", 300) + ".pdf": strings.Repeat("x", 196) + ".pdf",
	}
	for in, want := range cases {
		if got := SanitizeFilename(in, 2); got != want {
			t.Errorf("SanitizeFilename(%q) = %q, want %q", in, got, want)
		}
	}
}
