package outbound

import (
	"strings"

	"ganigeorgiev.com/pocketbase/pocketbase/core"

	"roost/server/internal/ingest"
)

// SignatureDelimiter opens a signature block (the RFC 3676 "-- " line). The
// composer looks for the same block to swap it when the From address changes.
const SignatureDelimiter = "-- \n"

// Identity is the owner's saved name and signature for one sending address.
type Identity struct {
	Address     string
	DisplayName string
	Signature   string
}

// LookupIdentity returns the saved identity for address, or a bare one.
func LookupIdentity(app core.App, address string) Identity {
	id := Identity{Address: strings.ToLower(address)}
	if id.Address == "" {
		return id
	}
	rec, err := app.FindFirstRecordByData("identities", "address", id.Address)
	if err != nil {
		return id
	}
	id.DisplayName = rec.GetString("display_name")
	id.Signature = rec.GetString("signature")
	return id
}

// SignatureBlock is the text a draft carries for sig, or "" for none.
func SignatureBlock(sig string) string {
	sig = strings.TrimRight(sig, " \n")
	if strings.TrimSpace(sig) == "" {
		return ""
	}
	return SignatureDelimiter + sig
}

// CanSend reports whether domain is one of the owner's domains with sending on.
func CanSend(app core.App, domain string) bool {
	rec, err := app.FindFirstRecordByData("domains", "domain", strings.ToLower(domain))
	return err == nil && rec.GetBool("send_enabled")
}

// DefaultFrom is the first saved identity (by address) on a domain that can
// send, or "" when there is none.
func DefaultFrom(app core.App) string {
	recs, err := app.FindRecordsByFilter("identities", "address != ''", "address", 0, 0)
	if err != nil {
		return ""
	}
	for _, r := range recs {
		if a := r.GetString("address"); CanSend(app, ingest.DomainOf(a)) {
			return a
		}
	}
	return ""
}

// applyIdentity sets a draft's From, display name and opening body for address.
// rest is what follows the signature (a quote or a forwarded message).
func applyIdentity(app core.App, d *core.Record, address, rest string) {
	id := LookupIdentity(app, address)
	d.Set("from_address", id.Address)
	name := id.DisplayName
	if name == "" {
		name = lastNameUsed(app, id.Address)
	}
	d.Set("from_name", name)
	body := "\n\n"
	if block := SignatureBlock(id.Signature); block != "" {
		body += block + "\n"
		if rest != "" {
			body += "\n"
		}
	}
	d.Set("text_body", body+rest)
}
