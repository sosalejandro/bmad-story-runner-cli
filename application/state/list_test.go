package state_test

import (
	"context"
	"path/filepath"
	"testing"

	appstate "github.com/sosalejandro/bmad-story-runner-cli/application/state"
	"github.com/sosalejandro/bmad-story-runner-cli/domain/state"
	"github.com/sosalejandro/bmad-story-runner-cli/infrastructure/state/sqlite"
)

func newServiceForTest(t *testing.T) (*appstate.StoryService, func()) {
	t.Helper()
	ctx := context.Background()
	db, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "bmad-state.db"))
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	svc := &appstate.StoryService{
		Stories:      sqlite.NewStoriesStore(db),
		Dependencies: sqlite.NewStoryDependenciesStore(db),
		Affects:      sqlite.NewStoryAffectsStore(db),
		Concerns:     sqlite.NewStoryConcernsStore(db),
		RetryCounts:  sqlite.NewStoryRetryCountsStore(db),
		Config:       sqlite.NewConfigStore(db),
		Checkpoints:  sqlite.NewCheckpointsStore(db),
	}
	return svc, func() { _ = db.Close() }
}

// TestList_NoFilter returns every story with empty blocker rows when no
// dependencies exist.
func TestList_NoFilter(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, cleanup := newServiceForTest(t)
	defer cleanup()

	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(svc.Stories.Insert(ctx, state.Story{
		ID: "1.1", Title: "alpha", Status: state.StatusPending,
		Complexity: state.ComplexityLow, StoryType: state.StoryTypeCode,
	}))
	must(svc.Stories.Insert(ctx, state.Story{
		ID: "1.2", Title: "beta", Status: state.StatusComplete,
		Complexity: state.ComplexityLow, StoryType: state.StoryTypeCode,
	}))

	rows, err := svc.List(ctx, appstate.ListFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	for _, r := range rows {
		if len(r.Blockers) != 0 {
			t.Fatalf("story %s: blockers = %d, want 0", r.Story.ID, len(r.Blockers))
		}
	}
}

// TestList_StatusFilter narrows by status.
func TestList_StatusFilter(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, cleanup := newServiceForTest(t)
	defer cleanup()

	for _, s := range []state.Story{
		{ID: "1.1", Status: state.StatusPending, Complexity: state.ComplexityLow, StoryType: state.StoryTypeCode},
		{ID: "1.2", Status: state.StatusComplete, Complexity: state.ComplexityLow, StoryType: state.StoryTypeCode},
		{ID: "1.3", Status: state.StatusPending, Complexity: state.ComplexityLow, StoryType: state.StoryTypeCode},
	} {
		if err := svc.Stories.Insert(ctx, s); err != nil {
			t.Fatalf("insert %s: %v", s.ID, err)
		}
	}

	pending := state.StatusPending
	rows, err := svc.List(ctx, appstate.ListFilter{Status: &pending})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("pending rows = %d, want 2", len(rows))
	}
	for _, r := range rows {
		if r.Story.Status != state.StatusPending {
			t.Fatalf("got status %q, want pending", r.Story.Status)
		}
	}
}

// TestList_UnblockedOnly excludes stories whose deps aren't all complete and
// marks each blocker with the correct resolution status.
func TestList_UnblockedOnly(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, cleanup := newServiceForTest(t)
	defer cleanup()

	for _, s := range []state.Story{
		{ID: "1.1", Status: state.StatusComplete, Complexity: state.ComplexityLow, StoryType: state.StoryTypeCode},
		{ID: "1.2", Status: state.StatusPending, Complexity: state.ComplexityLow, StoryType: state.StoryTypeCode},
		{ID: "2.1", Status: state.StatusPending, Complexity: state.ComplexityLow, StoryType: state.StoryTypeCode},
		{ID: "2.2", Status: state.StatusPending, Complexity: state.ComplexityLow, StoryType: state.StoryTypeCode},
	} {
		if err := svc.Stories.Insert(ctx, s); err != nil {
			t.Fatalf("insert %s: %v", s.ID, err)
		}
	}
	// 2.1 depends on 1.1 (resolved) → unblocked.
	// 2.2 depends on 1.2 (pending) → blocked.
	if err := svc.Dependencies.Add(ctx, "2.1", "1.1"); err != nil {
		t.Fatalf("dep 2.1->1.1: %v", err)
	}
	if err := svc.Dependencies.Add(ctx, "2.2", "1.2"); err != nil {
		t.Fatalf("dep 2.2->1.2: %v", err)
	}

	rows, err := svc.List(ctx, appstate.ListFilter{UnblockedOnly: true})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	gotIDs := map[string]bool{}
	for _, r := range rows {
		gotIDs[r.Story.ID] = true
	}
	if !gotIDs["1.1"] || !gotIDs["1.2"] || !gotIDs["2.1"] {
		t.Fatalf("unblocked rows missing expected ids: got %v", gotIDs)
	}
	if gotIDs["2.2"] {
		t.Fatalf("blocked story 2.2 should have been excluded by UnblockedOnly")
	}
}

// TestAddConcerns_SQLite stores one row per array element.
func TestAddConcerns_SQLite(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, cleanup := newServiceForTest(t)
	defer cleanup()

	if err := svc.Stories.Insert(ctx, state.Story{
		ID: "1.1", Title: "alpha", Status: state.StatusPending,
		Complexity: state.ComplexityLow, StoryType: state.StoryTypeCode,
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}

	entries := []appstate.AddConcernsInput{
		{"severity": "high", "note": "missing rollback test"},
		{"stage": "code-review", "finding": "flaky goroutine"},
	}
	res, err := svc.AddConcerns(ctx, "1.1", "code-review", entries)
	if err != nil {
		t.Fatalf("AddConcerns: %v", err)
	}
	if res.Added != 2 || res.StoryID != "1.1" || res.Source != "code-review" {
		t.Fatalf("result = %+v", res)
	}

	got, err := svc.Concerns.Of(ctx, "1.1")
	if err != nil {
		t.Fatalf("Concerns.Of: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("concerns rows = %d, want 2", len(got))
	}
	for _, c := range got {
		if c.Source != "code-review" {
			t.Fatalf("source = %q, want code-review", c.Source)
		}
		if c.BodyJSON == "" {
			t.Fatalf("body_json empty")
		}
	}
}

// TestAddConcerns_StoryNotFound surfaces the domain not-found error so the
// CLI can route it through cobra's normal error path (non-zero exit).
func TestAddConcerns_StoryNotFound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, cleanup := newServiceForTest(t)
	defer cleanup()

	_, err := svc.AddConcerns(ctx, "9.9", "cli", []appstate.AddConcernsInput{{"note": "x"}})
	if err == nil {
		t.Fatal("expected error for missing story, got nil")
	}
}
