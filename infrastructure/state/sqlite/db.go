// Package sqlite is the v6 state-store adapter. It implements the narrow
// ports declared in domain/state against a single SQLite file (WAL mode).
// All schema lives at infrastructure/state/sqlite/schema/ and is applied
// by Open via the embedded migration runner.
package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

// DB is the connection handle wrapper. Holds the underlying *sql.DB plus the
// path it was opened from (useful for diagnostics + cleanup-orphans flows).
type DB struct {
	conn *sql.DB
	path string
}

// Open opens (or creates) the SQLite file at path, applies any pending
// migrations, and returns a *DB ready for adapter use. Caller must Close.
//
// The DSN forces WAL journaling, enables foreign-key enforcement, and sets
// a busy timeout so concurrent reads don't fail spuriously while a writer
// holds the lock. One writer per process is the §3 contract; this just
// keeps readers patient.
func Open(ctx context.Context, path string) (*DB, error) {
	dsn := fmt.Sprintf(
		"file:%s?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)",
		path,
	)
	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("sqlite open %s: %w", path, err)
	}
	if err := conn.PingContext(ctx); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("sqlite ping %s: %w", path, err)
	}

	db := &DB{conn: conn, path: path}
	if err := db.migrate(ctx); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("sqlite migrate %s: %w", path, err)
	}
	return db, nil
}

// Close releases the underlying *sql.DB.
func (d *DB) Close() error { return d.conn.Close() }

// Path returns the on-disk path the DB was opened from.
func (d *DB) Path() string { return d.path }

// conn-accessor for adapters in this package.
func (d *DB) sqlDB() *sql.DB { return d.conn }
