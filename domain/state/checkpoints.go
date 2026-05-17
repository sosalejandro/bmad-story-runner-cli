package state

import (
	"context"
	"time"
)

// Checkpoints is the §12.5 dual-trigger record port. Fire writes a row with
// user_decision = NULL; the orchestrator's next loop iteration HALTs on the
// unresolved row, and Decide records the user's verdict.
type Checkpoints interface {
	Fire(ctx context.Context, c Checkpoint) (int64, error)
	Get(ctx context.Context, id int64) (Checkpoint, error)
	Unresolved(ctx context.Context) (*Checkpoint, error)
	Decide(ctx context.Context, id int64, decision CheckpointDecision, when time.Time) error
	StoriesSinceLast(ctx context.Context) (int, error)
}
