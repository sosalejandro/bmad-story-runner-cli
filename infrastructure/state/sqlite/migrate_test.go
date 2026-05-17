package sqlite_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/sosalejandro/bmad-story-runner-cli/infrastructure/state/sqlite"

	_ "modernc.org/sqlite"
)

// openRawForInspection opens a second read connection to the same file so
// tests can SELECT from system + payload tables without going through the
// adapter's accessors. The adapter package itself never exposes *sql.DB,
// which is correct — but the test layer needs to verify schema state.
func openRawForInspection(t *testing.T, path string) *sql.DB {
	t.Helper()
	conn, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		t.Fatalf("inspection open: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func TestMigrate_AppliesInitialSchema(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "migrate.db")

	db, err := sqlite.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	_ = db.Close()

	raw := openRawForInspection(t, path)

	// schema_version should have exactly one row at version 1.
	var version int
	if err := raw.QueryRow(`SELECT version FROM schema_version`).Scan(&version); err != nil {
		t.Fatalf("query schema_version: %v", err)
	}
	if version != 1 {
		t.Fatalf("schema_version.version = %d, want 1", version)
	}

	// Every spec-§3 table should exist after migration 0001.
	wantTables := []string{
		"config",
		"stories",
		"story_dependencies",
		"story_affects",
		"story_concerns",
		"story_retry_counts",
		"batches",
		"batch_stories",
		"worktrees",
		"env_allocations",
		"dispatches",
		"checkpoints",
		"depguard_flips",
		"depguard_flip_history",
	}
	for _, table := range wantTables {
		var name string
		err := raw.QueryRow(
			`SELECT name FROM sqlite_master WHERE type='table' AND name = ?`,
			table,
		).Scan(&name)
		if err != nil {
			t.Errorf("table %q missing after migration: %v", table, err)
		}
	}
}

func TestMigrate_IsIdempotent(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "idempotent.db")

	for i := 0; i < 3; i++ {
		db, err := sqlite.Open(context.Background(), path)
		if err != nil {
			t.Fatalf("Open iteration %d: %v", i, err)
		}
		if err := db.Close(); err != nil {
			t.Fatalf("Close iteration %d: %v", i, err)
		}
	}

	raw := openRawForInspection(t, path)
	var rowCount int
	if err := raw.QueryRow(`SELECT COUNT(*) FROM schema_version`).Scan(&rowCount); err != nil {
		t.Fatalf("count schema_version: %v", err)
	}
	if rowCount != 1 {
		t.Fatalf("schema_version row count after 3 opens = %d, want 1", rowCount)
	}
}
