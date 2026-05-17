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
}

// StoryDependencies is the m:n bridge between a story and its prerequisites.
type StoryDependencies interface {
	Add(ctx context.Context, storyID, dependsOnID string) error
	Remove(ctx context.Context, storyID, dependsOnID string) error
	Of(ctx context.Context, storyID string) ([]string, error)
	DependentsOf(ctx context.Context, storyID string) ([]string, error)
}

// StoryAffects records the file/dir paths a story touches (file-overlap
// detection for parallel batching).
type StoryAffects interface {
	Add(ctx context.Context, storyID, path string) error
	Remove(ctx context.Context, storyID, path string) error
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
