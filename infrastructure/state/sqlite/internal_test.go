package sqlite

import (
	"context"
	"strconv"
	"testing"
)

// BackdateClaimForTest is a test-only helper that reaches into the
// package's unexported *sql.DB to age a story's claimed_at column for
// stale-claim reaper tests (issue #21 gap 3). Lives in this file
// (package `sqlite`, not `sqlite_test`) so it can call sqlDB(); exported
// so the external `sqlite_test` package can invoke it without the helper
// leaking into the production API surface — `_test.go` files are
// stripped from non-test builds.
//
// Named `BackdateClaimForTest` (not `TestBackdate…`) so Go's test
// machinery doesn't mistake it for a `func(*testing.T)` test case.
func BackdateClaimForTest(t *testing.T, db *DB, id string, ageSeconds int) {
	t.Helper()
	_, err := db.sqlDB().ExecContext(context.Background(),
		`UPDATE stories
		    SET claimed_at = datetime('now', ?),
		        claimed_by = COALESCE(claimed_by, 'orchestrator')
		  WHERE id = ?`,
		"-"+strconv.Itoa(ageSeconds)+" seconds", id)
	if err != nil {
		t.Fatalf("BackdateClaimForTest %q age=%d: %v", id, ageSeconds, err)
	}
}
