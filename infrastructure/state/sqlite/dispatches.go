package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/sosalejandro/bmad-story-runner-cli/domain/state"
)

// DispatchesStore is the sqlite adapter for state.Dispatches.
type DispatchesStore struct{ db *DB }

func NewDispatchesStore(db *DB) *DispatchesStore { return &DispatchesStore{db: db} }

func (s *DispatchesStore) Insert(ctx context.Context, d state.Dispatch) (int64, error) {
	res, err := s.db.sqlDB().ExecContext(ctx, `
		INSERT INTO dispatches (
			story_id, stage, agent_role, attempt_no, status, reason,
			input_tokens, output_tokens, cache_read_tokens, cache_create_tokens,
			model, duration_ms, returned_at, idempotency_key
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		d.StoryID, string(d.Stage), d.AgentRole, d.AttemptNo, string(d.Status),
		nullString(d.Reason),
		d.Tokens.Input, d.Tokens.Output, d.Tokens.CacheRead, d.Tokens.CacheCreate,
		nullString(d.Model), d.DurationMS, nullTime(d.ReturnedAt),
		nullString(d.IdempotencyKey),
	)
	if err != nil {
		return 0, fmt.Errorf("dispatches insert: %w", err)
	}
	id, _ := res.LastInsertId()
	return id, nil
}

// MarkReturnedByKey updates the row whose idempotency_key matches. Returns
// state.ErrNotFound if no row carries the key.
func (s *DispatchesStore) MarkReturnedByKey(
	ctx context.Context,
	key string,
	status state.DispatchStatus,
	reason string,
	tokens state.TokenCounts,
	model string,
	durationMS int64,
	returnedAt time.Time,
) error {
	if key == "" {
		return fmt.Errorf("dispatches mark-returned-by-key: empty key")
	}
	var reasonArg sql.NullString
	if reason != "" {
		reasonArg = sql.NullString{String: reason, Valid: true}
	}
	var modelArg sql.NullString
	if model != "" {
		modelArg = sql.NullString{String: model, Valid: true}
	}
	// First-write-wins replay protection: only update rows that haven't been
	// returned yet. A second call with the same key matches zero rows and
	// surfaces as ErrNotFound — the caller knows the row is already settled.
	res, err := s.db.sqlDB().ExecContext(ctx, `
		UPDATE dispatches SET
			status = ?, reason = ?, input_tokens = ?, output_tokens = ?,
			cache_read_tokens = ?, cache_create_tokens = ?,
			model = ?, duration_ms = ?, returned_at = ?
		WHERE idempotency_key = ? AND returned_at IS NULL
	`,
		string(status), reasonArg, tokens.Input, tokens.Output,
		tokens.CacheRead, tokens.CacheCreate, modelArg, durationMS, returnedAt, key,
	)
	if err != nil {
		return fmt.Errorf("dispatches mark-returned-by-key: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return state.ErrNotFound
	}
	return nil
}

// GetByKey returns the dispatch row by idempotency_key. Read-only; used by
// `bmad dispatch record --key ... --hydrated-file` to discover the row's
// story_id + stage before mutating stories.hydrated_file.
func (s *DispatchesStore) GetByKey(ctx context.Context, key string) (state.Dispatch, error) {
	if key == "" {
		return state.Dispatch{}, fmt.Errorf("dispatches get-by-key: empty key")
	}
	row := s.db.sqlDB().QueryRowContext(ctx,
		`SELECT `+dispatchesSelectCols+` FROM dispatches WHERE idempotency_key = ?`, key)
	d, err := scanDispatch(row)
	if errors.Is(err, sql.ErrNoRows) {
		return state.Dispatch{}, state.ErrNotFound
	}
	if err != nil {
		return state.Dispatch{}, fmt.Errorf("dispatches get-by-key %q: %w", key, err)
	}
	return d, nil
}

// InFlight returns dispatches that were recorded (status=dispatched) but never
// returned. Crash-recovery uses this to drive reconciliation.
func (s *DispatchesStore) InFlight(ctx context.Context) ([]state.Dispatch, error) {
	rows, err := s.db.sqlDB().QueryContext(ctx,
		`SELECT `+dispatchesSelectCols+` FROM dispatches
		 WHERE status = ? AND returned_at IS NULL
		 ORDER BY id`, string(state.DispatchDispatched))
	if err != nil {
		return nil, fmt.Errorf("dispatches in-flight: %w", err)
	}
	defer rows.Close()
	var out []state.Dispatch
	for rows.Next() {
		d, err := scanDispatch(rows)
		if err != nil {
			return nil, fmt.Errorf("dispatches in-flight scan: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *DispatchesStore) MarkReturned(
	ctx context.Context,
	id int64,
	status state.DispatchStatus,
	reason string,
	tokens state.TokenCounts,
	model string,
	durationMS int64,
	returnedAt time.Time,
) error {
	var reasonArg sql.NullString
	if reason != "" {
		reasonArg = sql.NullString{String: reason, Valid: true}
	}
	var modelArg sql.NullString
	if model != "" {
		modelArg = sql.NullString{String: model, Valid: true}
	}

	res, err := s.db.sqlDB().ExecContext(ctx, `
		UPDATE dispatches SET
			status = ?, reason = ?, input_tokens = ?, output_tokens = ?,
			cache_read_tokens = ?, cache_create_tokens = ?,
			model = ?, duration_ms = ?, returned_at = ?
		WHERE id = ?
	`,
		string(status), reasonArg, tokens.Input, tokens.Output,
		tokens.CacheRead, tokens.CacheCreate, modelArg, durationMS, returnedAt, id,
	)
	if err != nil {
		return fmt.Errorf("dispatches mark-returned %d: %w", id, err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return state.ErrNotFound
	}
	return nil
}

const dispatchesSelectCols = `id, story_id, stage, agent_role, attempt_no, status, reason,
		input_tokens, output_tokens, cache_read_tokens, cache_create_tokens,
		model, duration_ms, dispatched_at, returned_at, idempotency_key`

func scanDispatch(row interface {
	Scan(dest ...any) error
}) (state.Dispatch, error) {
	var d state.Dispatch
	var reason, model, idemKey sql.NullString
	var returnedAt sql.NullTime
	var stage, status string
	if err := row.Scan(
		&d.ID, &d.StoryID, &stage, &d.AgentRole, &d.AttemptNo, &status, &reason,
		&d.Tokens.Input, &d.Tokens.Output, &d.Tokens.CacheRead, &d.Tokens.CacheCreate,
		&model, &d.DurationMS, &d.DispatchedAt, &returnedAt, &idemKey,
	); err != nil {
		return state.Dispatch{}, err
	}
	d.Stage = state.Stage(stage)
	d.Status = state.DispatchStatus(status)
	d.Reason = ptrString(reason)
	d.Model = ptrString(model)
	d.ReturnedAt = ptrTime(returnedAt)
	d.IdempotencyKey = ptrString(idemKey)
	return d, nil
}

func (s *DispatchesStore) LastForStory(ctx context.Context, storyID string, stage state.Stage) (*state.Dispatch, error) {
	row := s.db.sqlDB().QueryRowContext(ctx, `
		SELECT `+dispatchesSelectCols+` FROM dispatches
		WHERE story_id = ? AND stage = ?
		ORDER BY id DESC LIMIT 1
	`, storyID, string(stage))
	d, err := scanDispatch(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("dispatches last %q/%q: %w", storyID, stage, err)
	}
	return &d, nil
}

func (s *DispatchesStore) ListForStory(ctx context.Context, storyID string) ([]state.Dispatch, error) {
	rows, err := s.db.sqlDB().QueryContext(ctx,
		`SELECT `+dispatchesSelectCols+` FROM dispatches WHERE story_id = ? ORDER BY id`,
		storyID)
	if err != nil {
		return nil, fmt.Errorf("dispatches list: %w", err)
	}
	defer rows.Close()
	var out []state.Dispatch
	for rows.Next() {
		d, err := scanDispatch(rows)
		if err != nil {
			return nil, fmt.Errorf("dispatches scan: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *DispatchesStore) TokenRollupSince(ctx context.Context, since time.Time) (state.TokenCounts, error) {
	var t state.TokenCounts
	err := s.db.sqlDB().QueryRowContext(ctx, `
		SELECT COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0),
		       COALESCE(SUM(cache_read_tokens),0), COALESCE(SUM(cache_create_tokens),0)
		FROM dispatches WHERE dispatched_at >= ?
	`, since).Scan(&t.Input, &t.Output, &t.CacheRead, &t.CacheCreate)
	if err != nil {
		return state.TokenCounts{}, fmt.Errorf("dispatches rollup: %w", err)
	}
	return t, nil
}

func (s *DispatchesStore) TokenRollupForStory(ctx context.Context, storyID string) (state.TokenCounts, error) {
	var t state.TokenCounts
	err := s.db.sqlDB().QueryRowContext(ctx, `
		SELECT COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0),
		       COALESCE(SUM(cache_read_tokens),0), COALESCE(SUM(cache_create_tokens),0)
		FROM dispatches WHERE story_id = ?
	`, storyID).Scan(&t.Input, &t.Output, &t.CacheRead, &t.CacheCreate)
	if err != nil {
		return state.TokenCounts{}, fmt.Errorf("dispatches rollup %q: %w", storyID, err)
	}
	return t, nil
}
