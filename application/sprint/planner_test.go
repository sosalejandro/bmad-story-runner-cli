package sprint_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/sosalejandro/bmad-story-runner-cli/application/sprint"
	"github.com/sosalejandro/bmad-story-runner-cli/infrastructure/state/sqlite"
)

func openDB(t *testing.T) *sqlite.DB {
	t.Helper()
	db, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "bmad-state.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestPlan_TopoSortsRespectingDependencies(t *testing.T) {
	t.Parallel()
	db := openDB(t)
	planner := &sprint.Planner{
		Stories:      sqlite.NewStoriesStore(db),
		Dependencies: sqlite.NewStoryDependenciesStore(db),
		Affects:      sqlite.NewStoryAffectsStore(db),
		Batches:      sqlite.NewBatchesStore(db),
	}

	parsed := []sprint.ParsedStory{
		{Frontmatter: sprint.StoryFrontmatter{StoryID: "1.1"}},
		{Frontmatter: sprint.StoryFrontmatter{StoryID: "1.2", DependsOn: []string{"1.1"}}},
		{Frontmatter: sprint.StoryFrontmatter{StoryID: "1.3", DependsOn: []string{"1.1"}}},
		{Frontmatter: sprint.StoryFrontmatter{StoryID: "1.4", DependsOn: []string{"1.2", "1.3"}}},
	}
	res, err := planner.Plan(context.Background(), parsed, 10)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if res.BatchesCreated != 3 {
		t.Fatalf("batches = %d, want 3 (1.1; 1.2+1.3; 1.4)", res.BatchesCreated)
	}
}

func TestPlan_FileOverlapSplitsBatch(t *testing.T) {
	t.Parallel()
	db := openDB(t)
	planner := &sprint.Planner{
		Stories:      sqlite.NewStoriesStore(db),
		Dependencies: sqlite.NewStoryDependenciesStore(db),
		Affects:      sqlite.NewStoryAffectsStore(db),
		Batches:      sqlite.NewBatchesStore(db),
	}

	parsed := []sprint.ParsedStory{
		{Frontmatter: sprint.StoryFrontmatter{StoryID: "a", Affects: []string{"src/x/"}}},
		{Frontmatter: sprint.StoryFrontmatter{StoryID: "b", Affects: []string{"src/x/"}}}, // overlap
		{Frontmatter: sprint.StoryFrontmatter{StoryID: "c", Affects: []string{"src/y/"}}}, // disjoint
	}
	res, err := planner.Plan(context.Background(), parsed, 10)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if res.BatchesCreated != 2 {
		t.Fatalf("batches = %d, want 2 (a+c; b)", res.BatchesCreated)
	}
}

func TestPlan_AndroidStorySerializes(t *testing.T) {
	t.Parallel()
	db := openDB(t)
	planner := &sprint.Planner{
		Stories:      sqlite.NewStoriesStore(db),
		Dependencies: sqlite.NewStoryDependenciesStore(db),
		Affects:      sqlite.NewStoryAffectsStore(db),
		Batches:      sqlite.NewBatchesStore(db),
	}

	parsed := []sprint.ParsedStory{
		{Frontmatter: sprint.StoryFrontmatter{StoryID: "p", RequiresAndroid: true}},
		{Frontmatter: sprint.StoryFrontmatter{StoryID: "q"}},
	}
	res, err := planner.Plan(context.Background(), parsed, 10)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if res.BatchesCreated != 2 {
		t.Fatalf("batches = %d, want 2 (android-solo + the other)", res.BatchesCreated)
	}
}

func TestPlan_ReingestUpdatesStoriesPreservesProgress(t *testing.T) {
	t.Parallel()
	db := openDB(t)
	planner := &sprint.Planner{
		Stories:      sqlite.NewStoriesStore(db),
		Dependencies: sqlite.NewStoryDependenciesStore(db),
		Affects:      sqlite.NewStoryAffectsStore(db),
		Batches:      sqlite.NewBatchesStore(db),
	}
	ctx := context.Background()

	_, _ = planner.Plan(ctx, []sprint.ParsedStory{
		{Frontmatter: sprint.StoryFrontmatter{StoryID: "x.1", Title: "First", Complexity: "medium"}},
	}, 10)

	// User progresses the story.
	stories := sqlite.NewStoriesStore(db)
	if err := stories.SetComplete(ctx, "x.1", "abc", ""); err != nil {
		t.Fatalf("seed progress: %v", err)
	}

	// Re-plan with updated frontmatter (title changed).
	res, _ := planner.Plan(ctx, []sprint.ParsedStory{
		{Frontmatter: sprint.StoryFrontmatter{StoryID: "x.1", Title: "First — Renamed", Complexity: "high"}},
	}, 10)
	if res.StoriesUpdated != 1 {
		t.Fatalf("expected update, got result %+v", res)
	}

	got, _ := stories.Get(ctx, "x.1")
	if got.Title != "First — Renamed" {
		t.Errorf("title not updated: %q", got.Title)
	}
	if string(got.Status) != "complete" {
		t.Errorf("status reset on re-plan: %q (should preserve)", got.Status)
	}
}
