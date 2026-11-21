package store

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
)

// Full-text search over messages lives outside the collections snapshot: an FTS5
// external-content table plus triggers. The trigram tokenizer gives substring
// matching that also works for Chinese and Japanese, which have no word spaces.

const ftsDDL = "CREATE VIRTUAL TABLE IF NOT EXISTS messages_fts USING fts5(" +
	"subject, from_name, from_address, text_body, " +
	"content='messages', content_rowid='rowid', tokenize='trigram')"

var ftsTriggers = []string{
	`CREATE TRIGGER IF NOT EXISTS messages_fts_ai AFTER INSERT ON messages BEGIN
		INSERT INTO messages_fts(rowid, subject, from_name, from_address, text_body)
		VALUES (new.rowid, new.subject, new.from_name, new.from_address, new.text_body);
	END`,
	`CREATE TRIGGER IF NOT EXISTS messages_fts_ad AFTER DELETE ON messages BEGIN
		INSERT INTO messages_fts(messages_fts, rowid, subject, from_name, from_address, text_body)
		VALUES ('delete', old.rowid, old.subject, old.from_name, old.from_address, old.text_body);
	END`,
	`CREATE TRIGGER IF NOT EXISTS messages_fts_au AFTER UPDATE OF subject, from_name, from_address, text_body ON messages BEGIN
		INSERT INTO messages_fts(messages_fts, rowid, subject, from_name, from_address, text_body)
		VALUES ('delete', old.rowid, old.subject, old.from_name, old.from_address, old.text_body);
		INSERT INTO messages_fts(rowid, subject, from_name, from_address, text_body)
		VALUES (new.rowid, new.subject, new.from_name, new.from_address, new.text_body);
	END`,
}

var ftsTriggerNames = []string{"messages_fts_ai", "messages_fts_ad", "messages_fts_au"}

// Sync applies the schema snapshot and brings the search index in line with it.
// Triggers are dropped first: they name message columns, and a column the
// snapshot removes could not be dropped while a trigger still mentions it.
func Sync(app core.App) error {
	for _, name := range ftsTriggerNames {
		if _, err := app.DB().NewQuery("DROP TRIGGER IF EXISTS " + name).Execute(); err != nil {
			return err
		}
	}
	if err := ApplySchema(app); err != nil {
		return err
	}
	return ensureSearchIndex(app)
}

func ensureSearchIndex(app core.App) error {
	stmts := append([]string{
		ftsDDL,
		"CREATE TABLE IF NOT EXISTS _roost_meta (key TEXT PRIMARY KEY NOT NULL, value TEXT NOT NULL)",
	}, ftsTriggers...)
	for _, stmt := range stmts {
		if _, err := app.DB().NewQuery(stmt).Execute(); err != nil {
			return fmt.Errorf("search index: %w", err)
		}
	}

	// Rebuild when the schema or index definition changed (a table rebuild can
	// move rowids), so the index never silently drifts from the messages.
	sum := sha256.Sum256([]byte(string(schemaJSON) + ftsDDL + strings.Join(ftsTriggers, ";")))
	version := hex.EncodeToString(sum[:])
	var current string
	_ = app.DB().NewQuery("SELECT value FROM _roost_meta WHERE key = 'search_version'").Row(&current)
	if current == version {
		return nil
	}
	if _, err := app.DB().NewQuery("INSERT INTO messages_fts(messages_fts) VALUES ('rebuild')").Execute(); err != nil {
		return fmt.Errorf("rebuild search index: %w", err)
	}
	_, err := app.DB().NewQuery(
		"INSERT INTO _roost_meta (key, value) VALUES ('search_version', {:v}) " +
			"ON CONFLICT(key) DO UPDATE SET value = excluded.value").
		Bind(map[string]any{"v": version}).Execute()
	return err
}
