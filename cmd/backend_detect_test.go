package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestResolveStateBackend covers the SQLite-vs-legacy-JSON detection
// for `bmad list` / `bmad add-concerns` (issue #71).
func TestResolveStateBackend(t *testing.T) {
	t.Run("empty arg → SQLite", func(t *testing.T) {
		v6StatePathFlag = ""
		b, consumed := resolveStateBackend("")
		if b != backendSQLite || consumed {
			t.Fatalf("got (%v, %v), want (SQLite, false)", b, consumed)
		}
	})

	t.Run(".db suffix → SQLite, consumes arg", func(t *testing.T) {
		v6StatePathFlag = ""
		b, consumed := resolveStateBackend("./bmad-state.db")
		if b != backendSQLite || !consumed {
			t.Fatalf("got (%v, %v), want (SQLite, true)", b, consumed)
		}
		if v6StatePathFlag == "" || !strings.HasSuffix(v6StatePathFlag, "bmad-state.db") {
			t.Fatalf("v6StatePathFlag not set to absolute .db path: %q", v6StatePathFlag)
		}
	})

	t.Run(".json suffix → legacy JSON, does not consume arg", func(t *testing.T) {
		v6StatePathFlag = ""
		b, consumed := resolveStateBackend("./bmad-progress.json")
		if b != backendLegacyJSON || consumed {
			t.Fatalf("got (%v, %v), want (JSON, false)", b, consumed)
		}
		if v6StatePathFlag != "" {
			t.Fatalf("v6StatePathFlag should not be set, got %q", v6StatePathFlag)
		}
	})

	t.Run("no extension + SQLite magic bytes → SQLite", func(t *testing.T) {
		v6StatePathFlag = ""
		dir := t.TempDir()
		path := filepath.Join(dir, "no-ext-state")
		// SQLite header is "SQLite format 3\x00" — write it + some padding.
		if err := os.WriteFile(path, append([]byte("SQLite format 3\x00"), make([]byte, 100)...), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		b, consumed := resolveStateBackend(path)
		if b != backendSQLite || !consumed {
			t.Fatalf("got (%v, %v), want (SQLite, true)", b, consumed)
		}
	})

	t.Run("no extension + non-SQLite header → legacy JSON", func(t *testing.T) {
		v6StatePathFlag = ""
		dir := t.TempDir()
		path := filepath.Join(dir, "old-progress")
		if err := os.WriteFile(path, []byte(`{"stories":[]}`), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		b, consumed := resolveStateBackend(path)
		if b != backendLegacyJSON || consumed {
			t.Fatalf("got (%v, %v), want (JSON, false)", b, consumed)
		}
	})
}

// TestIsSQLiteFile covers the magic-byte sniff used by resolveStateBackend
// when the operator hands us a path with no obvious extension.
func TestIsSQLiteFile(t *testing.T) {
	t.Run("missing file → false", func(t *testing.T) {
		if isSQLiteFile(filepath.Join(t.TempDir(), "does-not-exist")) {
			t.Fatal("expected false for missing file")
		}
	})

	t.Run("too short → false", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "tiny")
		if err := os.WriteFile(path, []byte("hi"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		if isSQLiteFile(path) {
			t.Fatal("expected false for too-short file")
		}
	})
}
