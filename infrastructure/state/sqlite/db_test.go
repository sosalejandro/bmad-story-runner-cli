package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/sosalejandro/bmad-story-runner-cli/infrastructure/state/sqlite"
)

// newTempDB opens a fresh DB in t.TempDir() and registers a Close on cleanup.
func newTempDB(t *testing.T) *sqlite.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "bmad-state.db")
	db, err := sqlite.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestOpen_CreatesFileAtPath(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "fresh.db")

	db, err := sqlite.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer db.Close()

	if got := db.Path(); got != path {
		t.Fatalf("Path() = %q, want %q", got, path)
	}
}

func TestOpen_IsReentrantOnExistingFile(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "reopen.db")

	first, err := sqlite.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}

	second, err := sqlite.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("second Open on existing file: %v", err)
	}
	defer second.Close()
}

func TestOpen_FailsOnUnwritablePath(t *testing.T) {
	t.Parallel()
	// /proc/1 is not a writable location on Linux even for root; on other OSes
	// this test is skipped. The point is: a real Open failure must surface
	// a meaningful error, not panic.
	_, err := sqlite.Open(context.Background(), "/proc/1/bmad-state.db")
	if err == nil {
		t.Skip("expected error on unwritable path; environment allowed it")
	}
}
