package state

import "context"

// Envs is the per-story test-env allocation port. Backs `bmad env <verb>`.
// ReclaimReason on Release is one of "completed" | "stale" | "manual".
type Envs interface {
	Reserve(ctx context.Context, a EnvAllocation) error
	Get(ctx context.Context, storyID string) (EnvAllocation, error)
	Active(ctx context.Context) ([]EnvAllocation, error)
	InUsePorts(ctx context.Context) ([]int, error)
	RecordContainers(ctx context.Context, storyID string, containerIDs []string) error
	Release(ctx context.Context, storyID, reason string) error
}
