package state

import (
	"context"
	"time"
)

// Batches is the sprint-planning output port — ordered groups of stories
// that may execute in parallel. Insert assigns an ID and writes both the
// batches row and its batch_stories rows atomically.
type Batches interface {
	Insert(ctx context.Context, b Batch) (int64, error)
	Get(ctx context.Context, id int64) (Batch, error)
	NextPlanned(ctx context.Context) (*Batch, error)
	List(ctx context.Context) ([]Batch, error)
	MarkStarted(ctx context.Context, id int64, when time.Time) error
	MarkComplete(ctx context.Context, id int64, when time.Time) error
	ClearPlan(ctx context.Context) error
}
