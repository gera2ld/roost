package api

import (
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"ganigeorgiev.com/pocketbase/pocketbase/core"

	"roost/server/internal/outbound"
)

type createDraftBody struct {
	Mode   outbound.Mode `json:"mode"`
	Source string        `json:"source"`
	TZ     string        `json:"tz"` // IANA zone for the quoted date
}

func (h *handlers) createDraft(e *core.RequestEvent) error {
	var body createDraftBody
	if err := e.BindBody(&body); err != nil {
		return e.BadRequestError("invalid body", err)
	}
	if body.Mode == "" {
		body.Mode = outbound.New
	}
	if !slices.Contains([]outbound.Mode{outbound.New, outbound.Reply, outbound.ReplyAll, outbound.Forward}, body.Mode) {
		return e.BadRequestError("unknown mode", nil)
	}
	var src *core.Record
	if body.Mode != outbound.New {
		var err error
		if src, err = e.App.FindRecordById("messages", body.Source); err != nil || outbound.Editable(src) {
			return e.NotFoundError("source message not found", nil)
		}
	}
	loc, err := time.LoadLocation(body.TZ)
	if err != nil || body.TZ == "" {
		loc = time.UTC
	}
	draft, err := outbound.NewDraft(e.App, body.Mode, src, loc)
	if err != nil {
		return err
	}
	return e.JSON(http.StatusCreated, draft)
}

// draftPatch holds the editable fields; nil means unchanged. Recipient fields
// are the text as typed and are parsed leniently, so autosave never fails on a
// half-written address.
type draftPatch struct {
	FromName           *string   `json:"from_name"`
	FromAddress        *string   `json:"from_address"`
	To                 *string   `json:"to"`
	Cc                 *string   `json:"cc"`
	Bcc                *string   `json:"bcc"`
	Subject            *string   `json:"subject"`
	TextBody           *string   `json:"text_body"`
	ForwardAttachments *[]string `json:"forward_attachments"` // may only shrink
}

const maxDraftBody = 1 << 20

func (h *handlers) updateDraft(e *core.RequestEvent) error {
	draft, err := h.findDraft(e)
	if err != nil {
		return err
	}
	var p draftPatch
	if err := e.BindBody(&p); err != nil {
		return e.BadRequestError("invalid body", err)
	}
	if p.FromName != nil {
		draft.Set("from_name", strings.TrimSpace(*p.FromName))
	}
	if p.FromAddress != nil {
		draft.Set("from_address", strings.ToLower(strings.TrimSpace(*p.FromAddress)))
	}
	for field, v := range map[string]*string{"to": p.To, "cc": p.Cc, "bcc": p.Bcc} {
		if v != nil {
			draft.Set(field, outbound.ParseList(*v))
		}
	}
	if p.Subject != nil {
		draft.Set("subject", strings.TrimSpace(*p.Subject))
	}
	if p.TextBody != nil {
		if len(*p.TextBody) > maxDraftBody {
			return e.BadRequestError("message too long", nil)
		}
		draft.Set("text_body", *p.TextBody)
	}
	if p.ForwardAttachments != nil {
		var current []string
		_ = draft.UnmarshalJSONField("forward_attachments", &current)
		kept := []string{}
		for _, id := range *p.ForwardAttachments {
			if slices.Contains(current, id) {
				kept = append(kept, id)
			}
		}
		draft.Set("forward_attachments", kept)
	}
	if draft.GetString("send_status") == "failed" { // edited after a failure: a draft again
		draft.Set("send_status", "draft")
	}
	if err := e.App.Save(draft); err != nil {
		return err
	}
	return e.JSON(http.StatusOK, draft)
}

func (h *handlers) deleteDraft(e *core.RequestEvent) error {
	draft, err := h.findDraft(e)
	if err != nil {
		return err
	}
	if err := e.App.Delete(draft); err != nil {
		return err
	}
	return e.NoContent(http.StatusNoContent)
}

func (h *handlers) sendDraft(e *core.RequestEvent) error {
	if h.sender == nil {
		return e.Error(http.StatusServiceUnavailable, "sending is not configured (ROOST_RESEND_API_KEY)", nil)
	}
	if _, err := h.findDraft(e); err != nil {
		return err
	}
	msg, err := h.sender.Send(e.Request.Context(), e.Request.PathValue("id"))
	var apiErr *outbound.APIError
	switch {
	case errors.Is(err, outbound.ErrValidation):
		return e.BadRequestError(strings.TrimPrefix(err.Error(), outbound.ErrValidation.Error()+": "), nil)
	case errors.As(err, &apiErr) && apiErr.Status/100 == 4:
		return e.BadRequestError("Resend rejected the message: "+apiErr.Message, nil)
	case err != nil:
		return e.Error(http.StatusBadGateway, "Sending failed: "+err.Error(), nil)
	}
	return e.JSON(http.StatusOK, msg)
}

func (h *handlers) findDraft(e *core.RequestEvent) (*core.Record, error) {
	draft, err := e.App.FindRecordById("messages", e.Request.PathValue("id"))
	if err != nil || !outbound.Editable(draft) {
		return nil, e.NotFoundError("draft not found", nil)
	}
	return draft, nil
}
