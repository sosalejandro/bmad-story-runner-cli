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

// Issue #21 gap 1: replan must drop stale story_dependencies edges when a
// story's `depends_on` is relaxed in epics.md. Live repro from
// nutrition-v2-go: commit c9c00515 relaxed 11 Epic-2 stories to
// `depends_on: []`, ran `bmad sprint plan`, but the old rows persisted in
// SQLite and `bmad story next` kept skipping the stories because the
// dependency graph still said "wait on 1.4". Required a manual DELETE to
// unblock the sprint — that's the regression this test guards.
func TestPlan_IdempotentDependenciesOnRelax(t *testing.T) {
	t.Parallel()
	db := openDB(t)
	planner := &sprint.Planner{
		Stories:      sqlite.NewStoriesStore(db),
		Dependencies: sqlite.NewStoryDependenciesStore(db),
		Affects:      sqlite.NewStoryAffectsStore(db),
		Batches:      sqlite.NewBatchesStore(db),
	}
	ctx := context.Background()

	// Initial plan: 2.1 depends on 1.4. 1.4 is a stub with no frontmatter
	// (mirrors how an upstream-epic prerequisite typically looks in real
	// epics.md files — already-done external work referenced by id only).
	_, err := planner.Plan(ctx, []sprint.ParsedStory{
		{Frontmatter: sprint.StoryFrontmatter{StoryID: "1.4", Title: "Prereq"}},
		{Frontmatter: sprint.StoryFrontmatter{StoryID: "2.1", Title: "Cross-cutting", DependsOn: []string{"1.4"}}},
	}, 10)
	if err != nil {
		t.Fatalf("initial plan: %v", err)
	}

	deps := sqlite.NewStoryDependenciesStore(db)
	got, err := deps.Of(ctx, "2.1")
	if err != nil {
		t.Fatalf("deps.Of 2.1: %v", err)
	}
	if len(got) != 1 || got[0] != "1.4" {
		t.Fatalf("after initial plan: 2.1 deps = %v, want [1.4]", got)
	}

	// Operator relaxes 2.1 → depends_on: []. Replan.
	res, err := planner.Plan(ctx, []sprint.ParsedStory{
		{Frontmatter: sprint.StoryFrontmatter{StoryID: "1.4", Title: "Prereq"}},
		{Frontmatter: sprint.StoryFrontmatter{StoryID: "2.1", Title: "Cross-cutting", DependsOn: nil}},
	}, 10)
	if err != nil {
		t.Fatalf("replan: %v", err)
	}

	// 2.1 row should now have zero dependency edges.
	got2, err := deps.Of(ctx, "2.1")
	if err != nil {
		t.Fatalf("deps.Of 2.1 post-replan: %v", err)
	}
	if len(got2) != 0 {
		t.Errorf("after replan: 2.1 deps = %v, want []", got2)
	}

	// IngestResult should report the wipe so an operator can see it in stdout.
	if res.DependenciesCleared < 1 {
		t.Errorf("expected DependenciesCleared >= 1 (2.1 was wiped), got %d", res.DependenciesCleared)
	}
}

// Issue #21 gap 1: same idempotency contract for `affects`. If a story's
// `affects` list shrinks (e.g. a path is moved to a different story), the
// stale row must drop so file-overlap detection in `bmad story next`
// doesn't keep falsely splitting batches.
func TestPlan_IdempotentAffectsOnRelax(t *testing.T) {
	t.Parallel()
	db := openDB(t)
	planner := &sprint.Planner{
		Stories:      sqlite.NewStoriesStore(db),
		Dependencies: sqlite.NewStoryDependenciesStore(db),
		Affects:      sqlite.NewStoryAffectsStore(db),
		Batches:      sqlite.NewBatchesStore(db),
	}
	ctx := context.Background()

	_, err := planner.Plan(ctx, []sprint.ParsedStory{
		{Frontmatter: sprint.StoryFrontmatter{StoryID: "x.1", Affects: []string{"src/a/", "src/b/"}}},
	}, 10)
	if err != nil {
		t.Fatalf("initial plan: %v", err)
	}

	affects := sqlite.NewStoryAffectsStore(db)
	got, _ := affects.Of(ctx, "x.1")
	if len(got) != 2 {
		t.Fatalf("after initial plan: x.1 affects = %v, want 2 entries", got)
	}

	// Shrink to one path.
	res, err := planner.Plan(ctx, []sprint.ParsedStory{
		{Frontmatter: sprint.StoryFrontmatter{StoryID: "x.1", Affects: []string{"src/a/"}}},
	}, 10)
	if err != nil {
		t.Fatalf("replan: %v", err)
	}

	got2, _ := affects.Of(ctx, "x.1")
	if len(got2) != 1 || got2[0] != "src/a/" {
		t.Errorf("after replan: x.1 affects = %v, want [src/a/]", got2)
	}
	if res.AffectsCleared < 1 {
		t.Errorf("expected AffectsCleared >= 1, got %d", res.AffectsCleared)
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
