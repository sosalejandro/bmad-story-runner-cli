package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/sosalejandro/bmad-story-runner-cli/domain/state"
)

// WorktreesStore is the sqlite adapter for state.Worktrees.
type WorktreesStore struct{ db *DB }

func NewWorktreesStore(db *DB) *WorktreesStore { return &WorktreesStore{db: db} }

func scanWorktree(row interface {
	Scan(dest ...any) error
}) (state.Worktree, error) {
	var w state.Worktree
	var lastActivity sql.NullTime
	if err := row.Scan(&w.StoryID, &w.Path, &w.BranchName, &w.CreatedAt, &lastActivity); err != nil {
		return state.Worktree{}, err
	}
	w.LastActivityAt = ptrTime(lastActivity)
	return w, nil
}

func (s *WorktreesStore) Create(ctx context.Context, w state.Worktree) error {
	_, err := s.db.sqlDB().ExecContext(ctx, `
		INSERT INTO worktrees (story_id, path, branch_name, last_activity_at)
		VALUES (?, ?, ?, ?)
	`, w.StoryID, w.Path, w.BranchName, nullTime(w.LastActivityAt))
	if err != nil {
		return fmt.Errorf("worktree create %q: %w", w.StoryID, err)
	}
	return nil
}

func (s *WorktreesStore) Get(ctx context.Context, storyID string) (state.Worktree, error) {
	row := s.db.sqlDB().QueryRowContext(ctx, `
		SELECT story_id, path, branch_name, created_at, last_activity_at
		FROM worktrees WHERE story_id = ?
	`, storyID)
	w, err := scanWorktree(row)
	if errors.Is(err, sql.ErrNoRows) {
		return state.Worktree{}, state.ErrNotFound
	}
	if err != nil {
		return state.Worktree{}, fmt.Errorf("worktree get %q: %w", storyID, err)
	}
	return w, nil
}

func (s *WorktreesStore) List(ctx context.Context) ([]state.Worktree, error) {
	rows, err := s.db.sqlDB().QueryContext(ctx, `
		SELECT story_id, path, branch_name, created_at, last_activity_at
		FROM worktrees ORDER BY story_id
	`)
	if err != nil {
		return nil, fmt.Errorf("worktree list: %w", err)
	}
	defer rows.Close()
	var out []state.Worktree
	for rows.Next() {
		w, err := scanWorktree(rows)
		if err != nil {
			return nil, fmt.Errorf("worktree scan: %w", err)
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

func (s *WorktreesStore) Delete(ctx context.Context, storyID string) error {
	res, err := s.db.sqlDB().ExecContext(ctx, `DELETE FROM worktrees WHERE story_id = ?`, storyID)
	if err != nil {
		return fmt.Errorf("worktree delete %q: %w", storyID, err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return state.ErrNotFound
	}
	return nil
}

func (s *WorktreesStore) TouchActivity(ctx context.Context, storyID string, when time.Time) error {
	res, err := s.db.sqlDB().ExecContext(ctx,
		`UPDATE worktrees SET last_activity_at = ? WHERE story_id = ?`, when, storyID)
	if err != nil {
		return fmt.Errorf("worktree touch %q: %w", storyID, err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return state.ErrNotFound
	}
	return nil
}
