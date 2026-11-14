package store

import (
	"bytes"
	"os"
	"testing"

	"ganigeorgiev.com/pocketbase/pocketbase/tests"
)

// TestSchemaRoundTrip applies the snapshot to a fresh database and exports it again.
// The export must equal the embedded file byte for byte; otherwise schema.json is
// not canonical and field ids would drift. Run with ROOST_UPDATE_SCHEMA=1 to rewrite
// the file from the export.
func TestSchemaRoundTrip(t *testing.T) {
	app, err := tests.NewTestApp()
	if err != nil {
		t.Fatal(err)
	}
	defer app.Cleanup()

	if err := ApplySchema(app); err != nil {
		t.Fatal(err)
	}
	exported, err := ExportSchema(app)
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv("ROOST_UPDATE_SCHEMA") == "1" {
		if err := os.WriteFile("schema.json", exported, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	if !bytes.Equal(exported, schemaJSON) {
		t.Fatal("schema.json is not canonical; run: ROOST_UPDATE_SCHEMA=1 go test ./internal/store")
	}

	// Applying again must be a no-op that keeps the same ids.
	if err := ApplySchema(app); err != nil {
		t.Fatal(err)
	}
	again, err := ExportSchema(app)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(again, exported) {
		t.Fatal("re-applying the schema changed it")
	}
}
