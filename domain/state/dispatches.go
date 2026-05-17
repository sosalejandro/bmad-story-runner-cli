package state

import (
	"context"
	"time"
)

// Dispatches is the append-only L3-invocation log (one row per dispatch).
// Backs §12.7 token-cost tracking + crash-resume reconciliation via
// idempotency keys. Insert returns the assigned ID.
type Dispatches interface {
	Insert(ctx context.Context, d Dispatch) (int64, error)
	MarkReturned(ctx context.Context, id int64, status DispatchStatus, reason string, tokens TokenCounts, model string, durationMS int64, returnedAt time.Time) error

	// MarkReturnedByKey is the idempotency-aware variant — updates the row
	// whose idempotency_key matches, regardless of integer id. Returns
	// ErrNotFound if no row carries the key. Use this in the orchestrator's
	// post-Task() recording step, where the key was generated up-front.
	MarkReturnedByKey(ctx context.Context, idempotencyKey string, status DispatchStatus, reason string, tokens TokenCounts, model string, durationMS int64, returnedAt time.Time) error

	// InFlight returns dispatches that were recorded (status=dispatched) but
	// never returned (returned_at IS NULL). Crash-recovery reads this to
	// decide which rows to reconcile.
	InFlight(ctx context.Context) ([]Dispatch, error)

	LastForStory(ctx context.Context, storyID string, stage Stage) (*Dispatch, error)
	ListForStory(ctx context.Context, storyID string) ([]Dispatch, error)
	TokenRollupSince(ctx context.Context, since time.Time) (TokenCounts, error)
	TokenRollupForStory(ctx context.Context, storyID string) (TokenCounts, error)
}
