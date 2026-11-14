package api

import (
	"database/sql"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"unicode/utf8"

	"ganigeorgiev.com/pocketbase/pocketbase/core"

	"roost/server/internal/ingest"
	"roost/server/internal/outbound"
)

// Limits mirror schema.json so a too-long value is a clear 400, not a save error.
const (
	maxDisplayName = 200
	maxSignature   = 4000
)

var domainPattern = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$`)

type domainBody struct {
	Domain      string `json:"domain"`
	SendEnabled *bool  `json:"send_enabled"`
}

// addDomain records a domain before any mail has arrived for it (mail adds
// domains on its own). Adding one that exists returns it unchanged.
func (h *handlers) addDomain(e *core.RequestEvent) error {
	var body domainBody
	if err := e.BindBody(&body); err != nil {
		return e.BadRequestError("invalid body", err)
	}
	name := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(body.Domain)), ".")
	if len(name) > 253 || !domainPattern.MatchString(name) {
		return e.BadRequestError("enter a domain like example.com", nil)
	}
	rec, err := e.App.FindFirstRecordByData("domains", "domain", name)
	if err == nil {
		return e.JSON(http.StatusOK, rec)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	col, err := e.App.FindCollectionByNameOrId("domains")
	if err != nil {
		return err
	}
	rec = core.NewRecord(col)
	rec.Set("domain", name)
	if body.SendEnabled != nil {
		rec.Set("send_enabled", *body.SendEnabled)
	}
	if err := e.App.Save(rec); err != nil {
		return err
	}
	return e.JSON(http.StatusCreated, rec)
}

// updateDomain turns sending on or off for one domain.
func (h *handlers) updateDomain(e *core.RequestEvent) error {
	var body domainBody
	if err := e.BindBody(&body); err != nil || body.SendEnabled == nil {
		return e.BadRequestError("send_enabled is required", err)
	}
	rec, err := e.App.FindRecordById("domains", e.Request.PathValue("id"))
	if err != nil {
		return e.NotFoundError("domain not found", nil)
	}
	rec.Set("send_enabled", *body.SendEnabled)
	if err := e.App.Save(rec); err != nil {
		return err
	}
	return e.JSON(http.StatusOK, rec)
}

type identityBody struct {
	Address     string `json:"address"`
	DisplayName string `json:"display_name"`
	Signature   string `json:"signature"`
}

// saveIdentity creates or replaces the identity for one address on a known domain.
func (h *handlers) saveIdentity(e *core.RequestEvent) error {
	var body identityBody
	if err := e.BindBody(&body); err != nil {
		return e.BadRequestError("invalid body", err)
	}
	address := strings.ToLower(strings.TrimSpace(body.Address))
	if err := outbound.Valid(outbound.Address{Address: address}); err != nil {
		return e.BadRequestError("enter an address like you@example.com", nil)
	}
	domain := ingest.DomainOf(address)
	if _, err := e.App.FindFirstRecordByData("domains", "domain", domain); err != nil {
		return e.BadRequestError(domain+" is not one of your domains", nil)
	}
	name := strings.TrimSpace(body.DisplayName)
	sig := strings.ReplaceAll(strings.TrimRight(body.Signature, " \r\n"), "\r\n", "\n")
	if utf8.RuneCountInString(name) > maxDisplayName {
		return e.BadRequestError("display name is too long", nil)
	}
	if utf8.RuneCountInString(sig) > maxSignature {
		return e.BadRequestError("signature is too long", nil)
	}

	rec, err := e.App.FindFirstRecordByData("identities", "address", address)
	status := http.StatusOK
	if errors.Is(err, sql.ErrNoRows) {
		col, cerr := e.App.FindCollectionByNameOrId("identities")
		if cerr != nil {
			return cerr
		}
		rec = core.NewRecord(col)
		rec.Set("address", address)
		status = http.StatusCreated
	} else if err != nil {
		return err
	}
	rec.Set("display_name", name)
	rec.Set("signature", sig)
	if err := e.App.Save(rec); err != nil {
		return err
	}
	return e.JSON(status, rec)
}

func (h *handlers) deleteIdentity(e *core.RequestEvent) error {
	rec, err := e.App.FindRecordById("identities", e.Request.PathValue("id"))
	if err != nil {
		return e.NotFoundError("identity not found", nil)
	}
	if err := e.App.Delete(rec); err != nil {
		return err
	}
	return e.NoContent(http.StatusNoContent)
}
