package outbound

import (
	"fmt"
	"net/mail"
	"strings"

	"roost/server/internal/mailparse"
)

// Address is a stored recipient, as in the messages JSON fields.
type Address = mailparse.Address

// ParseList splits a recipient field as typed ("Ann <a@x.com>, b@y.com") into
// addresses. It is lenient so a half-typed draft still saves: a token that does
// not parse is kept as-is and rejected only at send time.
func ParseList(s string) []Address {
	out := []Address{}
	for _, tok := range splitAddresses(s) {
		if a, err := mail.ParseAddress(tok); err == nil {
			out = append(out, Address{Name: a.Name, Address: strings.ToLower(a.Address)})
		} else {
			out = append(out, Address{Address: tok})
		}
	}
	return out
}

// splitAddresses splits on commas and semicolons outside quotes and angle brackets.
func splitAddresses(s string) []string {
	var out []string
	var cur strings.Builder
	quoted, angle := false, false
	flush := func() {
		if t := strings.TrimSpace(cur.String()); t != "" {
			out = append(out, t)
		}
		cur.Reset()
	}
	for _, r := range s {
		switch {
		case r == '"':
			quoted = !quoted
		case r == '<' && !quoted:
			angle = true
		case r == '>' && !quoted:
			angle = false
		case (r == ',' || r == ';' || r == '\n') && !quoted && !angle:
			flush()
			continue
		}
		cur.WriteRune(r)
	}
	flush()
	return out
}

// Valid reports whether a stored address is a deliverable single mailbox.
func Valid(a Address) error {
	parsed, err := mail.ParseAddress(a.Address)
	if err != nil || parsed.Address != a.Address || !strings.Contains(a.Address, "@") {
		return fmt.Errorf("invalid address %q", a.Address)
	}
	return nil
}

// Format renders an address for a header or the provider (RFC 2047 names).
func Format(a Address) string {
	if a.Name == "" {
		return a.Address
	}
	return (&mail.Address{Name: a.Name, Address: a.Address}).String()
}

func formatAll(list []Address) []string {
	out := make([]string, 0, len(list))
	for _, a := range list {
		out = append(out, Format(a))
	}
	return out
}

func mailAddrs(list []Address) []mail.Address {
	out := make([]mail.Address, 0, len(list))
	for _, a := range list {
		out = append(out, mail.Address{Name: a.Name, Address: a.Address})
	}
	return out
}
