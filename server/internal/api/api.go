// Package api holds Roost's custom HTTP routes. Reads go through PocketBase's
// record API (collection rules allow the signed-in user); every write goes
// through these routes, so the collections stay read-only to clients.
package api

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"ganigeorgiev.com/pocketbase/pocketbase/apis"
	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/types"

	"roost/server/internal/ingest"
	"roost/server/internal/mailparse"
	"roost/server/internal/outbound"
	"roost/server/internal/spool"
	"roost/server/internal/store"
)

// Register mounts the authenticated routes under /api/roost. sender may be nil
// (no Resend key): drafts still work, sending answers 503.
func Register(se *core.ServeEvent, mailDir string, sender *outbound.Service) {
	h := &handlers{mailDir: mailDir, sender: sender}
	se.App.OnRecordAuthWithOAuth2Request("users").BindFunc(noOAuth2Signup)
	g := se.Router.Group("/api/roost")
	g.Bind(apis.RequireAuth("users"))
	g.POST("/threads/{id}/actions", h.threadAction)
	g.POST("/messages/{id}/actions", h.messageAction)
	g.GET("/search", h.search)
	g.GET("/mailboxes", h.mailboxes)
	g.GET("/attachments/{id}", h.attachment)
	g.POST("/drafts", h.createDraft)
	g.PATCH("/drafts/{id}", h.updateDraft)
	g.DELETE("/drafts/{id}", h.deleteDraft)
	g.POST("/drafts/{id}/send", h.sendDraft)
	g.POST("/domains", h.addDomain)
	g.PATCH("/domains/{id}", h.updateDomain)
	g.PUT("/identities", h.saveIdentity)
	g.DELETE("/identities/{id}", h.deleteIdentity)
}

type handlers struct {
	mailDir string
	sender  *outbound.Service
}

type actionBody struct {
	Action string `json:"action"`
}

func (h *handlers) threadAction(e *core.RequestEvent) error {
	var body actionBody
	if err := e.BindBody(&body); err != nil {
		return e.BadRequestError("invalid body", err)
	}
	thread, err := e.App.FindRecordById("threads", e.Request.PathValue("id"))
	if err != nil {
		return e.NotFoundError("thread not found", nil)
	}

	switch body.Action {
	case "read", "unread":
		err = setThreadRead(e.App, thread.Id, body.Action == "read")
	case "archive", "unarchive":
		thread.Set("is_archived", body.Action == "archive")
		err = e.App.Save(thread)
	case "trash":
		thread.Set("trashed_at", types.NowDateTime())
		err = e.App.Save(thread)
	case "restore":
		thread.Set("trashed_at", "")
		err = e.App.Save(thread)
	case "purge":
		if thread.GetString("trashed_at") == "" {
			return e.BadRequestError("only trashed threads can be purged", nil)
		}
		if err := ingest.PurgeThread(e.App, h.mailDir, thread.Id); err != nil {
			return err
		}
		return e.NoContent(http.StatusNoContent)
	default:
		return e.BadRequestError("unknown action", nil)
	}
	if err != nil {
		return err
	}
	fresh, err := e.App.FindRecordById("threads", thread.Id)
	if err != nil {
		return err
	}
	return e.JSON(http.StatusOK, fresh)
}

// setThreadRead marks every inbound message read, or only the newest one unread
// (so the thread shows as unread without losing what was read).
func setThreadRead(app core.App, threadID string, read bool) error {
	messages, err := app.FindRecordsByFilter("messages", "thread = {:t} && direction = 'inbound'",
		"-received_at", 0, 0, map[string]any{"t": threadID})
	if err != nil || len(messages) == 0 {
		return err
	}
	if !read {
		messages = messages[:1]
	}
	return app.RunInTransaction(func(tx core.App) error {
		for _, m := range messages {
			if m.GetBool("is_read") == read {
				continue
			}
			m.Set("is_read", read)
			if err := tx.Save(m); err != nil {
				return err
			}
		}
		return nil
	})
}

func (h *handlers) messageAction(e *core.RequestEvent) error {
	var body actionBody
	if err := e.BindBody(&body); err != nil {
		return e.BadRequestError("invalid body", err)
	}
	msg, err := e.App.FindRecordById("messages", e.Request.PathValue("id"))
	if err != nil {
		return e.NotFoundError("message not found", nil)
	}
	switch body.Action {
	case "read", "unread":
		msg.Set("is_read", body.Action == "read")
	case "star", "unstar":
		msg.Set("is_starred", body.Action == "star")
	default:
		return e.BadRequestError("unknown action", nil)
	}
	if err := e.App.Save(msg); err != nil {
		return err
	}
	return e.JSON(http.StatusOK, msg)
}

const maxSearchThreads = 50

// search returns threads (not trashed) containing messages that match q,
// most recent match first.
func (h *handlers) search(e *core.RequestEvent) error {
	q := strings.TrimSpace(e.Request.URL.Query().Get("q"))
	if q == "" {
		return e.JSON(http.StatusOK, []any{})
	}
	ids, err := store.Search(e.App, q, 500)
	if err != nil {
		return err
	}
	var threadIDs []string
	if len(ids) > 0 {
		messages, err := e.App.FindRecordsByIds("messages", ids)
		if err != nil {
			return err
		}
		byID := make(map[string]string, len(messages))
		for _, m := range messages {
			byID[m.Id] = m.GetString("thread")
		}
		for _, id := range ids { // keep the search order
			if t := byID[id]; t != "" && !slices.Contains(threadIDs, t) {
				threadIDs = append(threadIDs, t)
			}
		}
	}
	threads, err := e.App.FindRecordsByIds("threads", threadIDs)
	if err != nil {
		return err
	}
	byID := make(map[string]*core.Record, len(threads))
	for _, t := range threads {
		byID[t.Id] = t
	}
	out := make([]*core.Record, 0, maxSearchThreads)
	for _, id := range threadIDs {
		if t := byID[id]; t != nil && t.GetString("trashed_at") == "" {
			out = append(out, t)
			if len(out) == maxSearchThreads {
				break
			}
		}
	}
	return e.JSON(http.StatusOK, out)
}

// Mailbox is one receiving address with its counts.
type Mailbox struct {
	Address string `db:"mailbox" json:"address"`
	Domain  string `db:"mailbox_domain" json:"domain"`
	Total   int    `db:"total" json:"total"`
	Unread  int    `db:"unread" json:"unread"`
}

func (h *handlers) mailboxes(e *core.RequestEvent) error {
	var rows []Mailbox
	err := e.App.DB().NewQuery(`
		SELECT m.mailbox, m.mailbox_domain, COUNT(*) AS total,
		       SUM(CASE WHEN m.is_read = 0 AND m.direction = 'inbound' THEN 1 ELSE 0 END) AS unread
		FROM messages m LEFT JOIN threads t ON t.id = m.thread
		WHERE m.mailbox != '' AND (t.trashed_at IS NULL OR t.trashed_at = '')
		GROUP BY m.mailbox, m.mailbox_domain
		ORDER BY m.mailbox_domain, m.mailbox`).All(&rows)
	if err != nil {
		return err
	}
	if rows == nil {
		rows = []Mailbox{}
	}
	return e.JSON(http.StatusOK, rows)
}

// attachment streams one attachment, re-extracted from the raw message. It is
// always a download: the declared type is not trusted for rendering.
func (h *handlers) attachment(e *core.RequestEvent) error {
	att, err := e.App.FindRecordById("attachments", e.Request.PathValue("id"))
	if err != nil {
		return e.NotFoundError("attachment not found", nil)
	}
	msg, err := e.App.FindRecordById("messages", att.GetString("message"))
	if errors.Is(err, sql.ErrNoRows) {
		return e.NotFoundError("message not found", nil)
	}
	if err != nil {
		return err
	}
	key := msg.GetString("raw_path")
	if !spool.ValidKey(key) {
		return e.NotFoundError("raw message unavailable", nil)
	}
	raw, err := os.ReadFile(filepath.Join(h.mailDir, filepath.FromSlash(key)))
	if err != nil {
		return e.NotFoundError("raw message unavailable", nil)
	}
	content, err := mailparse.ExtractPart(raw, att.GetString("part_id"))
	if err != nil {
		return e.NotFoundError("attachment part unavailable", nil)
	}

	name := mailparse.SanitizeFilename(att.GetString("filename"), 0)
	header := e.Response.Header()
	header.Set("Content-Disposition", ContentDisposition(name))
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("Content-Security-Policy", "sandbox; default-src 'none'")
	header.Set("Cache-Control", "private, no-store")
	return e.Blob(http.StatusOK, "application/octet-stream", content)
}

// ContentDisposition builds an attachment header with an ASCII fallback and an
// RFC 5987 UTF-8 filename.
func ContentDisposition(name string) string {
	ascii := make([]rune, 0, len(name))
	for _, r := range name {
		if r < 0x20 || r > 0x7e || r == '"' || r == '\\' {
			r = '_'
		}
		ascii = append(ascii, r)
	}
	return fmt.Sprintf(`attachment; filename="%s"; filename*=UTF-8''%s`,
		string(ascii), strings.ReplaceAll(url.QueryEscape(name), "+", "%20"))
}
