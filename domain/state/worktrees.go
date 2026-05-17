package state

import (
	"context"
	"time"
)

// Worktrees is the per-story git-worktree lifecycle port. last_activity_at
// is refreshed by `env status` activity probes and consumed by the sweeper.
type Worktrees interface {
	Create(ctx context.Context, w Worktree) error
	Get(ctx context.Context, storyID string) (Worktree, error)
	List(ctx context.Context) ([]Worktree, error)
	Delete(ctx context.Context, storyID string) error
	TouchActivity(ctx context.Context, storyID string, when time.Time) error
}
