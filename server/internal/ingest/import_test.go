package ingest

import (
	"context"
	"path/filepath"
	"testing"
)

func TestImportFilesIdempotent(t *testing.T) {
	_, _, app := setup(t)
	dir := filepath.Join("..", "mailparse", "testdata")
	mailDir := t.TempDir()
	first, err := ImportFiles(context.Background(), app, mailDir, []string{dir}, "")
	if err != nil {
		t.Fatal(err)
	}
	if first.Stored == 0 || first.Failed != 0 {
		t.Fatalf("first import = %+v", first)
	}
	again, err := ImportFiles(context.Background(), app, mailDir, []string{dir}, "")
	if err != nil {
		t.Fatal(err)
	}
	if again.Stored != 0 || again.Skipped != first.Stored+first.Dupes {
		t.Errorf("second import = %+v (first %+v)", again, first)
	}
	if n := count(t, app, "messages", ""); n != first.Stored {
		t.Errorf("messages = %d, want %d", n, first.Stored)
	}
}
