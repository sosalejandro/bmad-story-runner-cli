package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/sosalejandro/bmad-story-runner-cli/domain/state"
)

// EnvsStore is the sqlite adapter for state.Envs (env_allocations table).
type EnvsStore struct{ db *DB }

func NewEnvsStore(db *DB) *EnvsStore { return &EnvsStore{db: db} }

const envSelectCols = `story_id, pg_port, redis_port, otel_port, db_name,
		container_ids, created_at, reclaimed_at, reclaim_reason`

func scanEnv(row interface {
	Scan(dest ...any) error
}) (state.EnvAllocation, error) {
	var (
		a            state.EnvAllocation
		otel         sql.NullInt64
		containerCSV string
		reclaimedAt  sql.NullTime
		reason       sql.NullString
	)
	if err := row.Scan(
		&a.StoryID, &a.PGPort, &a.RedisPort, &otel, &a.DBName,
		&containerCSV, &a.CreatedAt, &reclaimedAt, &reason,
	); err != nil {
		return state.EnvAllocation{}, err
	}
	a.OtelPort = ptrInt(otel)
	a.ContainerIDs = splitIDs(containerCSV)
	a.ReclaimedAt = ptrTime(reclaimedAt)
	a.ReclaimReason = ptrString(reason)
	return a, nil
}

func (s *EnvsStore) Reserve(ctx context.Context, a state.EnvAllocation) error {
	_, err := s.db.sqlDB().ExecContext(ctx, `
		INSERT INTO env_allocations (story_id, pg_port, redis_port, otel_port, db_name, container_ids)
		VALUES (?, ?, ?, ?, ?, ?)
	`, a.StoryID, a.PGPort, a.RedisPort, nullInt(a.OtelPort), a.DBName, joinIDs(a.ContainerIDs))
	if err != nil {
		return fmt.Errorf("env reserve %q: %w", a.StoryID, err)
	}
	return nil
}

func (s *EnvsStore) Get(ctx context.Context, storyID string) (state.EnvAllocation, error) {
	row := s.db.sqlDB().QueryRowContext(ctx,
		`SELECT `+envSelectCols+` FROM env_allocations WHERE story_id = ?`, storyID)
	a, err := scanEnv(row)
	if errors.Is(err, sql.ErrNoRows) {
		return state.EnvAllocation{}, state.ErrNotFound
	}
	if err != nil {
		return state.EnvAllocation{}, fmt.Errorf("env get %q: %w", storyID, err)
	}
	return a, nil
}

func (s *EnvsStore) Active(ctx context.Context) ([]state.EnvAllocation, error) {
	rows, err := s.db.sqlDB().QueryContext(ctx,
		`SELECT `+envSelectCols+` FROM env_allocations WHERE reclaimed_at IS NULL ORDER BY story_id`)
	if err != nil {
		return nil, fmt.Errorf("env active: %w", err)
	}
	defer rows.Close()
	var out []state.EnvAllocation
	for rows.Next() {
		a, err := scanEnv(rows)
		if err != nil {
			return nil, fmt.Errorf("env scan: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *EnvsStore) InUsePorts(ctx context.Context) ([]int, error) {
	rows, err := s.db.sqlDB().QueryContext(ctx, `
		SELECT pg_port FROM env_allocations WHERE reclaimed_at IS NULL
		UNION ALL
		SELECT redis_port FROM env_allocations WHERE reclaimed_at IS NULL
		UNION ALL
		SELECT otel_port FROM env_allocations WHERE reclaimed_at IS NULL AND otel_port IS NOT NULL
	`)
	if err != nil {
		return nil, fmt.Errorf("env in-use ports: %w", err)
	}
	defer rows.Close()
	var out []int
	for rows.Next() {
		var p int
		if err := rows.Scan(&p); err != nil {
			return nil, fmt.Errorf("env port scan: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *EnvsStore) RecordContainers(ctx context.Context, storyID string, containerIDs []string) error {
	res, err := s.db.sqlDB().ExecContext(ctx,
		`UPDATE env_allocations SET container_ids = ? WHERE story_id = ?`,
		joinIDs(containerIDs), storyID)
	if err != nil {
		return fmt.Errorf("env record-containers %q: %w", storyID, err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return state.ErrNotFound
	}
	return nil
}

func (s *EnvsStore) Release(ctx context.Context, storyID, reason string) error {
	res, err := s.db.sqlDB().ExecContext(ctx, `
		UPDATE env_allocations
		SET reclaimed_at = CURRENT_TIMESTAMP, reclaim_reason = ?
		WHERE story_id = ? AND reclaimed_at IS NULL
	`, reason, storyID)
	if err != nil {
		return fmt.Errorf("env release %q: %w", storyID, err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return state.ErrNotFound
	}
	return nil
}
