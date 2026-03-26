package fsutil

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

func TestCleanStaleFiles(t *testing.T) {
	dir := t.TempDir()
	log := zerolog.Nop()

	// Create an old file and a fresh file.
	old := filepath.Join(dir, "old.log")
	fresh := filepath.Join(dir, "fresh.log")
	if err := os.WriteFile(old, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fresh, []byte("fresh"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Backdate the old file.
	past := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}

	CleanStaleFiles(dir, 1*time.Hour, log)

	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Error("expected old file to be removed")
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Error("expected fresh file to remain")
	}
}

func TestCleanStaleFiles_MissingDir(t *testing.T) {
	// Should not panic on missing directory.
	CleanStaleFiles("/nonexistent/path", time.Hour, zerolog.Nop())
}
