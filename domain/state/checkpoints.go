package state

import (
	"context"
	"time"
)

// Checkpoints is the §12.5 dual-trigger record port. Fire writes a row with
// user_decision = NULL; the orchestrator's next loop iteration HALTs on the
// unresolved row, and Decide records the user's verdict.
//
// ResolveAllUnresolved is the bulk-clear used by `bmad sprint resume` (issue
// #71). It stamps every NULL-user_decision row with the supplied decision in
// a single statement so the orchestrator loop can't observe stale halt rows
// after the operator has explicitly resumed. Returns the number of rows
// affected (zero when no checkpoint was open — not an error).
type Checkpoints interface {
	Fire(ctx context.Context, c Checkpoint) (int64, error)
	Get(ctx context.Context, id int64) (Checkpoint, error)
	Unresolved(ctx context.Context) (*Checkpoint, error)
	Decide(ctx context.Context, id int64, decision CheckpointDecision, when time.Time) error
	ResolveAllUnresolved(ctx context.Context, decision CheckpointDecision, when time.Time) (int, error)
	StoriesSinceLast(ctx context.Context) (int, error)
}
