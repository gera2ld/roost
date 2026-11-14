package api

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestDomainSettings(t *testing.T) {
	e := newEnv(t)
	if rec := e.do("POST", "/api/roost/domains", `{"domain":"x.test"}`, false); rec.Code != http.StatusUnauthorized {
		t.Errorf("unauthenticated add = %d", rec.Code)
	}
	for _, bad := range []string{"", "localhost", "a b.test", "-x.test", "x..test"} {
		if code, _ := e.json("POST", "/api/roost/domains", fmt.Sprintf(`{"domain":%q}`, bad)); code != 400 {
			t.Errorf("add %q = %d, want 400", bad, code)
		}
	}
	code, d := e.json("POST", "/api/roost/domains", `{"domain":" Mail.Example.COM. "}`)
	if code != http.StatusCreated || d["domain"] != "mail.example.com" || d["send_enabled"] != false {
		t.Fatalf("add = %d %v", code, d)
	}
	if code, again := e.json("POST", "/api/roost/domains", `{"domain":"mail.example.com"}`); code != 200 || again["id"] != d["id"] {
		t.Errorf("re-add = %d %v", code, again)
	}
	id := d["id"].(string)
	if code, _ := e.json("PATCH", "/api/roost/domains/"+id, `{}`); code != 400 {
		t.Errorf("patch without send_enabled = %d", code)
	}
	if code, up := e.json("PATCH", "/api/roost/domains/"+id, `{"send_enabled":true}`); code != 200 || up["send_enabled"] != true {
		t.Errorf("enable = %d %v", code, up)
	}
	if code, _ := e.json("PATCH", "/api/roost/domains/nope", `{"send_enabled":true}`); code != 404 {
		t.Errorf("patch missing = %d", code)
	}
}

func TestIdentityValidationAndDelete(t *testing.T) {
	e := newEnv(t)
	for _, c := range []struct{ body, want string }{
		{`{"address":"not-an-address"}`, "enter an address"},
		{`{"address":"me@gmail.com"}`, "not one of your domains"},
		{fmt.Sprintf(`{"address":"me@roost.test","signature":%q}`, strings.Repeat("x", maxSignature+1)), "too long"},
	} {
		if code, out := e.json("PUT", "/api/roost/identities", c.body); code != 400 || !strings.Contains(strings.ToLower(fmt.Sprint(out["message"])), c.want) {
			t.Errorf("PUT %.40s = %d %v, want %q", c.body, code, out["message"], c.want)
		}
	}
	code, rec := e.json("PUT", "/api/roost/identities", `{"address":"Me@Roost.test","display_name":" Me ","signature":"Me\r\nroost.test\r\n\r\n"}`)
	if code != http.StatusCreated || rec["address"] != "me@roost.test" || rec["display_name"] != "Me" || rec["signature"] != "Me\nroost.test" {
		t.Fatalf("create = %d %v", code, rec)
	}
	if code, up := e.json("PUT", "/api/roost/identities", `{"address":"me@roost.test","display_name":"Me Again"}`); code != 200 || up["id"] != rec["id"] || up["signature"] != "" {
		t.Errorf("replace = %d %v", code, up)
	}
	if code, _ := e.json("DELETE", "/api/roost/identities/"+rec["id"].(string), ""); code != http.StatusNoContent {
		t.Errorf("delete = %d", code)
	}
	if code, _ := e.json("DELETE", "/api/roost/identities/"+rec["id"].(string), ""); code != 404 {
		t.Errorf("delete again = %d", code)
	}
}

func TestIdentityFillsDrafts(t *testing.T) {
	e := newEnv(t)
	e.json("PUT", "/api/roost/identities", `{"address":"me@roost.test","display_name":"Me","signature":"Me\nroost.test"}`)

	n := e.draft("new", "")
	if n["from_address"] != "me@roost.test" || n["from_name"] != "Me" || n["text_body"] != "\n\n-- \nMe\nroost.test\n" {
		t.Errorf("new draft = %q %q %q", n["from_address"], n["from_name"], n["text_body"])
	}
	r := e.draft("reply", e.message("plain-1@example.com").Id)
	body := r["text_body"].(string)
	if r["from_name"] != "Me" || !strings.HasPrefix(body, "\n\n-- \nMe\nroost.test\n\nOn ") {
		t.Errorf("reply draft name %q body %q", r["from_name"], body)
	}
	f := e.draft("forward", e.message("multi-1@example.com").Id)
	if fb := f["text_body"].(string); !strings.HasPrefix(fb, "\n\n-- \nMe\nroost.test\n\n---------- Forwarded") {
		t.Errorf("forward body = %q", fb)
	}
	// Without an identity there is no signature and the last name used stays the fallback.
	e.json("DELETE", "/api/roost/identities/"+identityID(t, e, "me@roost.test"), "")
	if b := e.draft("new", "")["text_body"]; b != "" {
		t.Errorf("new draft without identity body = %q", b)
	}
}

func TestReceiveOnlyDomain(t *testing.T) {
	e := newEnv(t)
	dom, err := e.app.FindFirstRecordByData("domains", "domain", "roost.test")
	if err != nil {
		t.Fatal(err)
	}
	e.json("PATCH", "/api/roost/domains/"+dom.Id, `{"send_enabled":false}`)

	src := e.message("plain-1@example.com") // received at me@roost.test
	d := e.draft("reply", src.Id)
	if d["from_address"] != "me@roost.test" {
		t.Errorf("with no sending identity the reply keeps the received address, got %q", d["from_address"])
	}
	id := d["id"].(string)
	if code, out := e.json("POST", "/api/roost/drafts/"+id+"/send", ""); code != 400 || !strings.Contains(fmt.Sprint(out["message"]), "receive-only") {
		t.Errorf("send from receive-only domain = %d %v", code, out)
	}

	e.json("POST", "/api/roost/domains", `{"domain":"send.test","send_enabled":true}`)
	e.json("PUT", "/api/roost/identities", `{"address":"me@send.test","display_name":"Sender"}`)
	all := e.draft("reply_all", src.Id)
	if all["from_address"] != "me@send.test" || all["from_name"] != "Sender" {
		t.Errorf("reply from receive-only mailbox = %q %q", all["from_address"], all["from_name"])
	}
	for _, a := range append(addresses(all["to"]), addresses(all["cc"])...) {
		if a == "me@roost.test" || a == "me@send.test" {
			t.Errorf("own address %s in reply-all recipients", a)
		}
	}
	if len(e.sent.emails) != 0 {
		t.Error("a receive-only domain reached the provider")
	}
}

func identityID(t *testing.T, e *env, address string) string {
	t.Helper()
	rec, err := e.app.FindFirstRecordByData("identities", "address", address)
	if err != nil {
		t.Fatal(err)
	}
	return rec.Id
}
