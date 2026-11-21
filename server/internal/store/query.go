package store

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/types"
)

// Query is one item of a parsed search query, exactly as @gera2ld/filter-query
// emits it in the web client: a term ({field, query, negate}) or a group
// ({group, negate}). Top-level items are OR'd, a group's items are AND'd.
// Field names are not checked by the parser; binding them happens here.
type Query struct {
	Field  *string `json:"field"`
	Query  string  `json:"query"`
	Negate bool    `json:"negate"`
	Group  []Query `json:"group"`
}

// SearchOptions tune SearchThreads. Loc resolves dates such as after:2026-01-02
// to the user's midnight; Now anchors relative dates such as after:7d.
type SearchOptions struct {
	Limit int
	Loc   *time.Location
	Now   time.Time
}

// QueryError is a query the user can fix (an unknown is: value, a bad date).
type QueryError struct{ msg string }

func (e *QueryError) Error() string { return e.msg }

func badQuery(format string, args ...any) error {
	return &QueryError{msg: fmt.Sprintf(format, args...)}
}

// IsQueryError reports whether err is the user's query, not the database.
func IsQueryError(err error) bool {
	var q *QueryError
	return errors.As(err, &q)
}

const (
	maxQueryTerms = 64
	maxQueryDepth = 16
)

// ftsAll is what a plain term searches: every column of messages_fts.
var ftsAll = []string{"subject", "from_name", "from_address", "text_body"}

// SearchThreads returns ids of threads with a message matching the query, most
// recent match first. Every term tests one message (joined with its thread's
// flags); terms never combine matches from different messages of a thread.
// Trashed threads are left out unless the query asks for is:trash.
func SearchThreads(app core.App, items []Query, opt SearchOptions) ([]string, error) {
	if opt.Limit <= 0 {
		opt.Limit = 50
	}
	if opt.Loc == nil {
		opt.Loc = time.UTC
	}
	if opt.Now.IsZero() {
		opt.Now = time.Now()
	}
	c := &compiler{params: map[string]any{"limit": opt.Limit}, opt: opt}
	expr, err := c.items(items, "OR", 0, false)
	if err != nil || expr == "" {
		// A query whose every term dropped out still meant to filter: matching
		// nothing is right, matching everything would silently widen it.
		return nil, err
	}
	where := expr
	if !c.wantsTrash {
		where += " AND COALESCE(t.trashed_at, '') = ''"
	}
	var ids []string
	err = app.DB().NewQuery(
		"SELECT m.thread FROM messages m JOIN threads t ON t.id = m.thread WHERE " + where +
			" GROUP BY m.thread ORDER BY MAX(m.received_at) DESC LIMIT {:limit}").
		Bind(c.params).Column(&ids)
	return ids, err
}

type compiler struct {
	params     map[string]any
	opt        SearchOptions
	n          int
	terms      int
	wantsTrash bool
}

func (c *compiler) param(v any) string {
	c.n++
	name := "q" + strconv.Itoa(c.n)
	c.params[name] = v
	return "{:" + name + "}"
}

// like binds a substring LIKE pattern for value.
func (c *compiler) like(value string) string {
	return c.param("%"+likeEscaper.Replace(value)+"%") + ` ESCAPE '\'`
}

// items joins each item's predicate with op; "" means every item dropped out.
// negated tracks whether an odd number of ! encloses these items.
func (c *compiler) items(items []Query, op string, depth int, negated bool) (string, error) {
	if depth > maxQueryDepth {
		return "", badQuery("query nests too deeply")
	}
	var parts []string
	for _, it := range items {
		neg := negated != it.Negate
		var s string
		var err error
		if it.Group != nil {
			s, err = c.items(it.Group, "AND", depth+1, neg)
		} else {
			s, err = c.term(it, neg)
		}
		if err != nil {
			return "", err
		}
		if s == "" {
			continue
		}
		if it.Negate {
			s = "(NOT " + s + ")"
		}
		parts = append(parts, s)
	}
	switch len(parts) {
	case 0:
		return "", nil
	case 1:
		return parts[0], nil
	}
	return "(" + strings.Join(parts, " "+op+" ") + ")", nil
}

func (c *compiler) term(it Query, negated bool) (string, error) {
	value := strings.TrimSpace(it.Query)
	if value == "" {
		return "", nil
	}
	if c.terms++; c.terms > maxQueryTerms {
		return "", badQuery("too many search terms (at most %d)", maxQueryTerms)
	}
	if it.Field == nil {
		return c.text(ftsAll, value), nil
	}
	switch field := strings.ToLower(*it.Field); field {
	case "subject":
		return c.text([]string{"subject"}, value), nil
	case "from":
		return c.text([]string{"from_name", "from_address"}, value), nil
	case "body":
		return c.text([]string{"text_body"}, value), nil
	case "to", "cc":
		p := c.like(value)
		return fmt.Sprintf(`(EXISTS (SELECT 1 FROM json_each(%s) j WHERE `+
			`json_extract(j.value, '$.address') LIKE %s OR json_extract(j.value, '$.name') LIKE %s))`,
			jsonArray(`m."`+field+`"`), p, p), nil
	case "mailbox":
		return "(m.mailbox LIKE " + c.like(value) + ")", nil
	case "domain":
		return "(m.mailbox_domain = " + c.param(strings.TrimPrefix(value, "@")) + " COLLATE NOCASE)", nil
	case "filename":
		return "(EXISTS (SELECT 1 FROM attachments a WHERE a.message = m.id AND a.filename LIKE " +
			c.like(value) + "))", nil
	case "has":
		switch strings.ToLower(value) {
		case "attachment", "attachments":
			return "(EXISTS (SELECT 1 FROM attachments a WHERE a.message = m.id AND " +
				"COALESCE(a.disposition, '') != 'inline'))", nil
		}
		return "", badQuery("unknown has: value %q (try has:attachment)", value)
	case "is":
		return c.is(strings.ToLower(value), negated)
	case "label":
		return fmt.Sprintf("(EXISTS (SELECT 1 FROM json_each(%s) j JOIN labels l ON l.id = j.value "+
			"WHERE l.name = %s COLLATE NOCASE))", jsonArray("m.labels"), c.param(value)), nil
	case "after", "before":
		at, err := c.date(value)
		if err != nil {
			return "", err
		}
		op := ">="
		if field == "before" {
			op = "<"
		}
		return "(m.received_at " + op + " " + c.param(at.UTC().Format(types.DefaultDateLayout)) + ")", nil
	}
	// An unknown field is plain text that happened to hold a colon: a URL, a
	// time of day, "Re:". Matching nothing would hide exactly that mail.
	return c.text(ftsAll, *it.Field+":"+value), nil
}

var isValues = map[string]string{
	"unread":   "m.is_read = 0",
	"read":     "m.is_read = 1",
	"starred":  "m.is_starred = 1",
	"sent":     "m.direction = 'outbound'",
	"archived": "t.is_archived = 1",
	"trash":    "COALESCE(t.trashed_at, '') != ''",
	"bounced":  "m.delivery_status = 'bounced'",
}

func (c *compiler) is(value string, negated bool) (string, error) {
	sql, ok := isValues[value]
	if !ok {
		return "", badQuery("unknown is: value %q (try unread, read, starred, sent, archived, trash, bounced)", value)
	}
	if value == "trash" && !negated {
		c.wantsTrash = true
	}
	return "(" + sql + ")", nil
}

// text matches value as a substring of any of the FTS columns. Terms shorter
// than three characters cannot use the trigram index and scan with LIKE.
func (c *compiler) text(cols []string, value string) string {
	if utf8.RuneCountInString(value) < 3 {
		p := c.like(value)
		ors := make([]string, len(cols))
		for i, col := range cols {
			ors[i] = "m." + col + " LIKE " + p
		}
		return "(" + strings.Join(ors, " OR ") + ")"
	}
	// A quoted FTS5 string is a phrase: no operators, no syntax errors.
	match := `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
	if len(cols) < len(ftsAll) {
		match = "{" + strings.Join(cols, " ") + "} : " + match
	}
	return "(m.rowid IN (SELECT rowid FROM messages_fts WHERE messages_fts MATCH " + c.param(match) + "))"
}

// jsonArray guards json_each against a column that holds no valid JSON.
func jsonArray(col string) string {
	return "CASE WHEN json_valid(" + col + ") THEN " + col + " ELSE '[]' END"
}

var relativeDate = regexp.MustCompile(`^(\d{1,4})([dwmy])$`)

// date reads YYYY-MM-DD (midnight in the user's zone) or a relative age such
// as 7d, 2w, 3m or 1y (that long before now).
func (c *compiler) date(value string) (time.Time, error) {
	if m := relativeDate.FindStringSubmatch(strings.ToLower(value)); m != nil {
		n, _ := strconv.Atoi(m[1])
		switch m[2] {
		case "d":
			return c.opt.Now.AddDate(0, 0, -n), nil
		case "w":
			return c.opt.Now.AddDate(0, 0, -7*n), nil
		case "m":
			return c.opt.Now.AddDate(0, -n, 0), nil
		default:
			return c.opt.Now.AddDate(-n, 0, 0), nil
		}
	}
	at, err := time.ParseInLocation("2006-01-02", value, c.opt.Loc)
	if err != nil {
		return time.Time{}, badQuery("bad date %q (use YYYY-MM-DD, or an age like 7d, 2w, 3m, 1y)", value)
	}
	return at, nil
}

// likeEscaper makes a value literal inside a LIKE pattern with ESCAPE '\'.
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
