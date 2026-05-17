package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/sosalejandro/bmad-story-runner-cli/domain/state"
)

// CheckpointsStore is the sqlite adapter for state.Checkpoints (§12.5).
type CheckpointsStore struct{ db *DB }

func NewCheckpointsStore(db *DB) *CheckpointsStore { return &CheckpointsStore{db: db} }

func (s *CheckpointsStore) Fire(ctx context.Context, c state.Checkpoint) (int64, error) {
	res, err := s.db.sqlDB().ExecContext(ctx, `
		INSERT INTO checkpoints (trigger_kind, trigger_detail, stories_since_last, summary_json)
		VALUES (?, ?, ?, ?)
	`, string(c.TriggerKind), nullString(c.TriggerDetail), c.StoriesSinceLast, c.SummaryJSON)
	if err != nil {
		return 0, fmt.Errorf("checkpoints fire: %w", err)
	}
	id, _ := res.LastInsertId()
	return id, nil
}

func scanCheckpoint(row interface {
	Scan(dest ...any) error
}) (state.Checkpoint, error) {
	var c state.Checkpoint
	var triggerDetail sql.NullString
	var userDecision sql.NullString
	var decidedAt sql.NullTime
	if err := row.Scan(
		&c.ID, &c.TriggeredAt, &c.TriggerKind, &triggerDetail,
		&c.StoriesSinceLast, &userDecision, &decidedAt, &c.SummaryJSON,
	); err != nil {
		return state.Checkpoint{}, err
	}
	c.TriggerDetail = ptrString(triggerDetail)
	c.DecidedAt = ptrTime(decidedAt)
	if userDecision.Valid {
		d := state.CheckpointDecision(userDecision.String)
		c.UserDecision = &d
	}
	return c, nil
}

const checkpointCols = `id, triggered_at, trigger_kind, trigger_detail,
		stories_since_last, user_decision, decided_at, summary_json`

func (s *CheckpointsStore) Get(ctx context.Context, id int64) (state.Checkpoint, error) {
	row := s.db.sqlDB().QueryRowContext(ctx,
		`SELECT `+checkpointCols+` FROM checkpoints WHERE id = ?`, id)
	c, err := scanCheckpoint(row)
	if errors.Is(err, sql.ErrNoRows) {
		return state.Checkpoint{}, state.ErrNotFound
	}
	if err != nil {
		return state.Checkpoint{}, fmt.Errorf("checkpoints get %d: %w", id, err)
	}
	return c, nil
}

func (s *CheckpointsStore) Unresolved(ctx context.Context) (*state.Checkpoint, error) {
	row := s.db.sqlDB().QueryRowContext(ctx,
		`SELECT `+checkpointCols+` FROM checkpoints
		 WHERE user_decision IS NULL
		 ORDER BY id DESC LIMIT 1`)
	c, err := scanCheckpoint(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("checkpoints unresolved: %w", err)
	}
	return &c, nil
}

func (s *CheckpointsStore) Decide(ctx context.Context, id int64, decision state.CheckpointDecision, when time.Time) error {
	res, err := s.db.sqlDB().ExecContext(ctx, `
		UPDATE checkpoints SET user_decision = ?, decided_at = ?
		WHERE id = ? AND user_decision IS NULL
	`, string(decision), when, id)
	if err != nil {
		return fmt.Errorf("checkpoints decide %d: %w", id, err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return state.ErrNotFound
	}
	return nil
}

func (s *CheckpointsStore) StoriesSinceLast(ctx context.Context) (int, error) {
	// "Since last checkpoint" = stories with completed_at > MAX(triggered_at).
	// If no checkpoints exist yet, count all completed stories.
	var n int
	err := s.db.sqlDB().QueryRowContext(ctx, `
		SELECT COUNT(*) FROM stories
		WHERE status = ?
		  AND (completed_at IS NULL
		       OR completed_at > COALESCE((SELECT MAX(triggered_at) FROM checkpoints), '1970-01-01'))
	`, string(state.StatusComplete)).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("checkpoints stories-since-last: %w", err)
	}
	return n, nil
}
