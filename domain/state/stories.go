package state

import "context"

// StoryFilter narrows a List query. Empty fields are "any".
type StoryFilter struct {
	Status        *Status
	CurrentStage  *Stage
	HasEnv        *bool
	ParallelGroup *int
	IDs           []string
}

// Stories is the core story-row port — Get/List/Insert/Update + lifecycle
// transitions. Per ISP, sibling concerns live in their own ports:
// dependencies, affects, concerns, retry-counts.
type Stories interface {
	Get(ctx context.Context, id string) (Story, error)
	List(ctx context.Context, f StoryFilter) ([]Story, error)
	Insert(ctx context.Context, s Story) error
	Update(ctx context.Context, s Story) error
	SetStatus(ctx context.Context, id string, status Status) error
	SetCurrentStage(ctx context.Context, id string, stage *Stage) error
	SetComplete(ctx context.Context, id string, commitHash, prURL string) error

	// ClaimUnclaimedPending atomically picks up to `max` unclaimed stories
	// whose status is pending AND whose ids are in `eligibleIDs`, marks them
	// claimed by `claimedBy` with claimed_at = now, and returns the picked rows.
	// Two concurrent callers cannot both claim the same story — the write
	// transaction serializes the check + update.
	//
	// `eligibleIDs` should be pre-filtered by the caller (deps satisfied,
	// affects disjoint, etc.) — this method only enforces "unclaimed +
	// pending + in the candidate set."
	ClaimUnclaimedPending(ctx context.Context, eligibleIDs []string, max int, claimedBy string) ([]Story, error)

	// ReleaseClaim clears claimed_at + claimed_by for the given story —
	// called on completion, on error-after-claim, or by the orchestrator's
	// crash-resume reconciler when it decides to give a story back.
	ReleaseClaim(ctx context.Context, id string) error
}

// StoryDependencies is the m:n bridge between a story and its prerequisites.
type StoryDependencies interface {
	Add(ctx context.Context, storyID, dependsOnID string) error
	Remove(ctx context.Context, storyID, dependsOnID string) error
	// RemoveAllFor deletes every dependency edge for storyID. Used by
	// `bmad sprint plan` to make re-ingest idempotent: when an operator
	// relaxes `depends_on` for a story (e.g. ["1.4"] → []), the planner
	// must drop the stale row(s) before re-inserting whatever the new
	// frontmatter declares. Without this, `bmad story next` continues to
	// see the old (unsatisfied) edge and skips the story forever.
	// No-op (not an error) if the story has no dependency rows.
	RemoveAllFor(ctx context.Context, storyID string) error
	Of(ctx context.Context, storyID string) ([]string, error)
	DependentsOf(ctx context.Context, storyID string) ([]string, error)
}

// StoryAffects records the file/dir paths a story touches (file-overlap
// detection for parallel batching).
type StoryAffects interface {
	Add(ctx context.Context, storyID, path string) error
	Remove(ctx context.Context, storyID, path string) error
	// RemoveAllFor deletes every affects row for storyID. Used by
	// `bmad sprint plan` to make re-ingest idempotent: when an operator
	// removes a path from a story's `affects` list, the planner must
	// drop the stale row(s) before re-inserting the new set — otherwise
	// file-overlap detection in `bmad story next` keeps splitting batches
	// for a path that's no longer claimed. No-op (not an error) if the
	// story has no affects rows.
	RemoveAllFor(ctx context.Context, storyID string) error
	Of(ctx context.Context, storyID string) ([]string, error)
	StoriesAffecting(ctx context.Context, path string) ([]string, error)
}

// StoryConcerns is the append-only QA-concerns log per story.
type StoryConcerns interface {
	Add(ctx context.Context, storyID, source, bodyJSON string) error
	Of(ctx context.Context, storyID string) ([]Concern, error)
}

// StoryRetryCounts tracks per-stage retry budgets.
type StoryRetryCounts interface {
	Get(ctx context.Context, storyID string) (RetryCounts, error)
	IncTDD(ctx context.Context, storyID string) error
	IncQA(ctx context.Context, storyID string) error
	IncCI(ctx context.Context, storyID string) error
	IncReview(ctx context.Context, storyID string) error
	Reset(ctx context.Context, storyID string) error
}
