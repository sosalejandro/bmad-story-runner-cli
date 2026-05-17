package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/sosalejandro/bmad-story-runner-cli/domain/state"
)

// BatchesStore is the sqlite adapter for state.Batches. Insert is transactional
// — both the batches row and its batch_stories children commit together.
type BatchesStore struct{ db *DB }

func NewBatchesStore(db *DB) *BatchesStore { return &BatchesStore{db: db} }

func (s *BatchesStore) Insert(ctx context.Context, b state.Batch) (int64, error) {
	tx, err := s.db.sqlDB().BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("batches insert begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx,
		`INSERT INTO batches (sequence_no, status) VALUES (?, ?)`,
		b.SequenceNo, string(b.Status))
	if err != nil {
		return 0, fmt.Errorf("batches insert row: %w", err)
	}
	id, _ := res.LastInsertId()

	for _, sid := range b.StoryIDs {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO batch_stories (batch_id, story_id) VALUES (?, ?)`,
			id, sid); err != nil {
			return 0, fmt.Errorf("batch_stories insert %q: %w", sid, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("batches insert commit: %w", err)
	}
	return id, nil
}

func (s *BatchesStore) Get(ctx context.Context, id int64) (state.Batch, error) {
	var b state.Batch
	var started, completed sql.NullTime
	err := s.db.sqlDB().QueryRowContext(ctx, `
		SELECT id, sequence_no, status, created_at, started_at, completed_at
		FROM batches WHERE id = ?
	`, id).Scan(&b.ID, &b.SequenceNo, &b.Status, &b.CreatedAt, &started, &completed)
	if errors.Is(err, sql.ErrNoRows) {
		return state.Batch{}, state.ErrNotFound
	}
	if err != nil {
		return state.Batch{}, fmt.Errorf("batches get %d: %w", id, err)
	}
	b.StartedAt = ptrTime(started)
	b.CompletedAt = ptrTime(completed)

	ids, err := queryIDs(ctx, s.db,
		`SELECT story_id FROM batch_stories WHERE batch_id = ? ORDER BY story_id`, id)
	if err != nil {
		return state.Batch{}, fmt.Errorf("batch_stories load: %w", err)
	}
	b.StoryIDs = ids
	return b, nil
}

func (s *BatchesStore) NextPlanned(ctx context.Context) (*state.Batch, error) {
	var id int64
	err := s.db.sqlDB().QueryRowContext(ctx, `
		SELECT id FROM batches WHERE status = ? ORDER BY sequence_no LIMIT 1
	`, string(state.BatchPlanned)).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("batches next-planned: %w", err)
	}
	b, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	return &b, nil
}

func (s *BatchesStore) List(ctx context.Context) ([]state.Batch, error) {
	rows, err := s.db.sqlDB().QueryContext(ctx,
		`SELECT id FROM batches ORDER BY sequence_no`)
	if err != nil {
		return nil, fmt.Errorf("batches list: %w", err)
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("batches list scan: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make([]state.Batch, 0, len(ids))
	for _, id := range ids {
		b, err := s.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, nil
}

func (s *BatchesStore) MarkStarted(ctx context.Context, id int64, when time.Time) error {
	res, err := s.db.sqlDB().ExecContext(ctx,
		`UPDATE batches SET status = ?, started_at = ? WHERE id = ?`,
		string(state.BatchInFlight), when, id)
	if err != nil {
		return fmt.Errorf("batches mark-started %d: %w", id, err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return state.ErrNotFound
	}
	return nil
}

func (s *BatchesStore) MarkComplete(ctx context.Context, id int64, when time.Time) error {
	res, err := s.db.sqlDB().ExecContext(ctx,
		`UPDATE batches SET status = ?, completed_at = ? WHERE id = ?`,
		string(state.BatchComplete), when, id)
	if err != nil {
		return fmt.Errorf("batches mark-complete %d: %w", id, err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return state.ErrNotFound
	}
	return nil
}

func (s *BatchesStore) ClearPlan(ctx context.Context) error {
	_, err := s.db.sqlDB().ExecContext(ctx,
		`DELETE FROM batches WHERE status = ?`, string(state.BatchPlanned))
	if err != nil {
		return fmt.Errorf("batches clear-plan: %w", err)
	}
	return nil
}
