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
			model, duration_ms, returned_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		d.StoryID, string(d.Stage), d.AgentRole, d.AttemptNo, string(d.Status),
		nullString(d.Reason),
		d.Tokens.Input, d.Tokens.Output, d.Tokens.CacheRead, d.Tokens.CacheCreate,
		nullString(d.Model), d.DurationMS, nullTime(d.ReturnedAt),
	)
	if err != nil {
		return 0, fmt.Errorf("dispatches insert: %w", err)
	}
	id, _ := res.LastInsertId()
	return id, nil
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
		model, duration_ms, dispatched_at, returned_at`

func scanDispatch(row interface {
	Scan(dest ...any) error
}) (state.Dispatch, error) {
	var d state.Dispatch
	var reason, model sql.NullString
	var returnedAt sql.NullTime
	var stage, status string
	if err := row.Scan(
		&d.ID, &d.StoryID, &stage, &d.AgentRole, &d.AttemptNo, &status, &reason,
		&d.Tokens.Input, &d.Tokens.Output, &d.Tokens.CacheRead, &d.Tokens.CacheCreate,
		&model, &d.DurationMS, &d.DispatchedAt, &returnedAt,
	); err != nil {
		return state.Dispatch{}, err
	}
	d.Stage = state.Stage(stage)
	d.Status = state.DispatchStatus(status)
	d.Reason = ptrString(reason)
	d.Model = ptrString(model)
	d.ReturnedAt = ptrTime(returnedAt)
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
