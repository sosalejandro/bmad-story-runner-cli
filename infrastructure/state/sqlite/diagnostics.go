package sqlite

import (
	"context"
	"fmt"
	"sort"
)

// AppliedSchemaVersions returns the schema versions present in the
// schema_version table, ascending.
//
// This is a diagnostics helper (used by `bmad doctor`) and intentionally
// does NOT live on a domain/state port. The schema-version concept is an
// infrastructure detail of the SQLite adapter, not a business-level port,
// so callers reach into the concrete package on purpose. If a second
// backend ever appears, we'd surface a `HealthProbe` port instead — but
// premature abstraction is a worse failure mode than direct coupling for
// a self-check command.
func AppliedSchemaVersions(ctx context.Context, db *DB) ([]int, error) {
	rows, err := db.conn.QueryContext(ctx, `SELECT version FROM schema_version`)
	if err != nil {
		return nil, fmt.Errorf("query schema_version: %w", err)
	}
	defer rows.Close()

	var out []int
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return nil, fmt.Errorf("scan schema_version row: %w", err)
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate schema_version rows: %w", err)
	}
	sort.Ints(out)
	return out, nil
}
