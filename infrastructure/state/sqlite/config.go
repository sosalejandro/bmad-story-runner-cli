package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/sosalejandro/bmad-story-runner-cli/domain/state"
)

// ConfigStore is the sqlite adapter for state.Config (spec §7 runtime knobs).
type ConfigStore struct {
	db *DB
}

// NewConfigStore wires a ConfigStore around an already-opened *DB.
func NewConfigStore(db *DB) *ConfigStore { return &ConfigStore{db: db} }

// Get returns the value for key. Returns state.ErrNotFound if absent.
func (c *ConfigStore) Get(ctx context.Context, key string) (string, error) {
	var v string
	err := c.db.sqlDB().QueryRowContext(ctx, `SELECT value FROM config WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", state.ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("config get %q: %w", key, err)
	}
	return v, nil
}

// Set upserts key=value. updated_at is refreshed.
func (c *ConfigStore) Set(ctx context.Context, key, value string) error {
	_, err := c.db.sqlDB().ExecContext(ctx, `
		INSERT INTO config (key, value, updated_at)
		VALUES (?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(key) DO UPDATE SET
		  value = excluded.value,
		  updated_at = CURRENT_TIMESTAMP
	`, key, value)
	if err != nil {
		return fmt.Errorf("config set %q: %w", key, err)
	}
	return nil
}

// All returns every config row, ordered by key for stable output.
func (c *ConfigStore) All(ctx context.Context) ([]state.ConfigEntry, error) {
	rows, err := c.db.sqlDB().QueryContext(ctx, `SELECT key, value, updated_at FROM config ORDER BY key`)
	if err != nil {
		return nil, fmt.Errorf("config all: %w", err)
	}
	defer rows.Close()

	var out []state.ConfigEntry
	for rows.Next() {
		var e state.ConfigEntry
		if err := rows.Scan(&e.Key, &e.Value, &e.UpdatedAt); err != nil {
			return nil, fmt.Errorf("config scan: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Delete removes the key. No-op (not an error) if the key was not set.
func (c *ConfigStore) Delete(ctx context.Context, key string) error {
	_, err := c.db.sqlDB().ExecContext(ctx, `DELETE FROM config WHERE key = ?`, key)
	if err != nil {
		return fmt.Errorf("config delete %q: %w", key, err)
	}
	return nil
}
