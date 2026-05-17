package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/sosalejandro/bmad-story-runner-cli/domain/state"
)

// DepguardStore is the sqlite adapter for state.Depguard. Flip writes both
// the current-state row (upsert) and an audit-log row atomically.
type DepguardStore struct{ db *DB }

func NewDepguardStore(db *DB) *DepguardStore { return &DepguardStore{db: db} }

func (s *DepguardStore) Get(ctx context.Context, rule string) (state.DepguardFlip, error) {
	var f state.DepguardFlip
	err := s.db.sqlDB().QueryRowContext(ctx,
		`SELECT rule, state, flipped_at FROM depguard_flips WHERE rule = ?`, rule).
		Scan(&f.Rule, &f.State, &f.FlippedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return state.DepguardFlip{}, state.ErrNotFound
	}
	if err != nil {
		return state.DepguardFlip{}, fmt.Errorf("depguard get %q: %w", rule, err)
	}
	return f, nil
}

func (s *DepguardStore) All(ctx context.Context) ([]state.DepguardFlip, error) {
	rows, err := s.db.sqlDB().QueryContext(ctx,
		`SELECT rule, state, flipped_at FROM depguard_flips ORDER BY rule`)
	if err != nil {
		return nil, fmt.Errorf("depguard all: %w", err)
	}
	defer rows.Close()
	var out []state.DepguardFlip
	for rows.Next() {
		var f state.DepguardFlip
		if err := rows.Scan(&f.Rule, &f.State, &f.FlippedAt); err != nil {
			return nil, fmt.Errorf("depguard scan: %w", err)
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (s *DepguardStore) Flip(ctx context.Context, rule string, to state.DepguardState, reason string) error {
	tx, err := s.db.sqlDB().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("depguard flip begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Read prior state (NULL = first flip).
	var from sql.NullString
	if err := tx.QueryRowContext(ctx,
		`SELECT state FROM depguard_flips WHERE rule = ?`, rule).Scan(&from); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("depguard flip read-prior %q: %w", rule, err)
	}
	fromVal := "" // empty = pre-existing rule had no recorded state
	if from.Valid {
		fromVal = from.String
	}

	// Upsert current state.
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO depguard_flips (rule, state, flipped_at) VALUES (?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(rule) DO UPDATE SET state = excluded.state, flipped_at = CURRENT_TIMESTAMP
	`, rule, string(to)); err != nil {
		return fmt.Errorf("depguard flip upsert %q: %w", rule, err)
	}

	// Append audit row.
	var reasonArg sql.NullString
	if reason != "" {
		reasonArg = sql.NullString{String: reason, Valid: true}
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO depguard_flip_history (rule, from_state, to_state, reason)
		VALUES (?, ?, ?, ?)
	`, rule, fromVal, string(to), reasonArg); err != nil {
		return fmt.Errorf("depguard flip history %q: %w", rule, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("depguard flip commit: %w", err)
	}
	return nil
}

func (s *DepguardStore) History(ctx context.Context, rule string) ([]state.DepguardFlipEvent, error) {
	rows, err := s.db.sqlDB().QueryContext(ctx, `
		SELECT id, rule, from_state, to_state, flipped_at, reason
		FROM depguard_flip_history WHERE rule = ? ORDER BY id
	`, rule)
	if err != nil {
		return nil, fmt.Errorf("depguard history: %w", err)
	}
	defer rows.Close()
	var out []state.DepguardFlipEvent
	for rows.Next() {
		var e state.DepguardFlipEvent
		var reason sql.NullString
		if err := rows.Scan(&e.ID, &e.Rule, &e.From, &e.To, &e.FlippedAt, &reason); err != nil {
			return nil, fmt.Errorf("depguard history scan: %w", err)
		}
		e.Reason = ptrString(reason)
		out = append(out, e)
	}
	return out, rows.Err()
}
