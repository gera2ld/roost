// Package store owns Roost's PocketBase schema.
//
// There are no migrations: schema.json is the schema. It is embedded in the binary
// and applied in full on every server start, so what it declares is what exists and
// what it omits is removed (with its records).
//
// To change the schema: edit collections in the admin UI (or schema.json directly),
// then regenerate the canonical file and commit it:
//
//	go run . schema export > internal/store/schema.json
package store

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
)

//go:embed schema.json
var schemaJSON []byte

// SchemaJSON returns the embedded collections snapshot.
func SchemaJSON() []byte { return schemaJSON }

// ApplySchema makes the database match the embedded snapshot exactly, except for the
// users collection's sign-in settings, which are kept or taken from the environment
// (see AuthConfig).
// Authoritative in both directions: declared fields and collections are created or
// restored, undeclared ones are deleted with their data.
func ApplySchema(app core.App) error {
	cfg, err := envAuthConfig()
	if err != nil {
		return err
	}
	return applySchema(app, cfg)
}

func applySchema(app core.App, cfg AuthConfig) error {
	current, err := app.FindCollectionByNameOrId(usersCollection)
	if err != nil {
		current = nil // fresh database
	}
	snapshot, err := withAuthConfig(schemaJSON, cfg, current)
	if err != nil {
		return err
	}
	if err := app.ImportCollectionsByMarshaledJSON(snapshot, true); err != nil {
		return fmt.Errorf("apply schema snapshot: %w", err)
	}
	return nil
}

// ExportSchema renders the database's collections in the canonical shape schema.json
// holds, so re-exporting an unchanged database yields a byte-identical file.
func ExportSchema(app core.App) ([]byte, error) {
	collections, err := app.FindAllCollections()
	if err != nil {
		return nil, err
	}
	document := make([]any, 0, len(collections))
	for _, collection := range collections {
		// Every non-system collection belongs in the snapshot: the import deletes
		// whatever it omits.
		if collection.System {
			continue
		}
		encoded, err := json.Marshal(collection)
		if err != nil {
			return nil, err
		}
		var entry map[string]any
		if err := json.Unmarshal(encoded, &entry); err != nil {
			return nil, err
		}
		document = append(document, entry)
	}
	raw, err := json.Marshal(document)
	if err != nil {
		return nil, err
	}
	return CanonicalSnapshot(raw)
}

// CanonicalSnapshot normalizes an exported collections document (a bare array or
// the list endpoint's {"items": [...]} envelope) into exactly the bytes schema.json
// holds: system collections and per-instance noise dropped, sorted by name.
func CanonicalSnapshot(raw []byte) ([]byte, error) {
	document, err := decodeCollectionsDocument(raw)
	if err != nil {
		return nil, err
	}
	entries := make([]map[string]any, 0, len(document))
	for i, item := range document {
		entry, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("entry %d is not an object", i)
		}
		if system, _ := entry["system"].(bool); system {
			continue
		}
		entries = append(entries, normalizeEntry(entry))
	}
	slices.SortFunc(entries, func(a, b map[string]any) int {
		left, _ := a["name"].(string)
		right, _ := b["name"].(string)
		return strings.Compare(left, right)
	})
	encoded, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return nil, err
	}
	// Trailing newline so the bytes match the file on disk exactly.
	return append(encoded, '\n'), nil
}

func decodeCollectionsDocument(raw []byte) ([]any, error) {
	var probe any
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, fmt.Errorf("collections export is not JSON: %w", err)
	}
	switch typed := probe.(type) {
	case []any:
		return typed, nil
	case map[string]any:
		items, ok := typed["items"].([]any)
		if !ok {
			return nil, fmt.Errorf("object has no items array; expected a collections list")
		}
		return items, nil
	default:
		return nil, fmt.Errorf("expected an array of collections or an object with items")
	}
}

// normalizeEntry drops per-instance noise from one exported collection.
func normalizeEntry(entry map[string]any) map[string]any {
	out := make(map[string]any, len(entry))
	for key, value := range entry {
		if value == nil {
			continue
		}
		out[key] = value
	}
	delete(out, "created")
	delete(out, "updated")
	delete(out, "system")
	resetAuthOptions(out)
	if kind, _ := out["type"].(string); kind == string(core.CollectionTypeView) {
		delete(out, "fields")
		delete(out, "indexes")
	} else {
		delete(out, "viewQuery")
	}
	return out
}
