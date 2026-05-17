package state

import (
	"context"
	"time"
)

// Dispatches is the append-only L3-invocation log (one row per dispatch).
// Backs §12.7 token-cost tracking. Insert returns the assigned ID.
type Dispatches interface {
	Insert(ctx context.Context, d Dispatch) (int64, error)
	MarkReturned(ctx context.Context, id int64, status DispatchStatus, reason string, tokens TokenCounts, model string, durationMS int64, returnedAt time.Time) error
	LastForStory(ctx context.Context, storyID string, stage Stage) (*Dispatch, error)
	ListForStory(ctx context.Context, storyID string) ([]Dispatch, error)
	TokenRollupSince(ctx context.Context, since time.Time) (TokenCounts, error)
	TokenRollupForStory(ctx context.Context, storyID string) (TokenCounts, error)
}
