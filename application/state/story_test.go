package state_test

import (
	"context"
	"path/filepath"
	"testing"

	appstate "github.com/sosalejandro/bmad-story-runner-cli/application/state"
	"github.com/sosalejandro/bmad-story-runner-cli/domain/state"
	"github.com/sosalejandro/bmad-story-runner-cli/infrastructure/state/sqlite"
)

func newStoryService(t *testing.T) (*appstate.StoryService, *sqlite.DB) {
	t.Helper()
	db, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "bmad-state.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return &appstate.StoryService{
		Stories:      sqlite.NewStoriesStore(db),
		Dependencies: sqlite.NewStoryDependenciesStore(db),
		Affects:      sqlite.NewStoryAffectsStore(db),
		Concerns:     sqlite.NewStoryConcernsStore(db),
		RetryCounts:  sqlite.NewStoryRetryCountsStore(db),
		Config:       sqlite.NewConfigStore(db),
		Checkpoints:  sqlite.NewCheckpointsStore(db),
	}, db
}

func seed(t *testing.T, svc *appstate.StoryService, id string, status state.Status) {
	t.Helper()
	if err := svc.Stories.Insert(context.Background(), state.Story{
		ID: id, File: id + ".md", Title: id, Status: status,
		Complexity: state.ComplexityMedium,
	}); err != nil {
		t.Fatalf("seed %q: %v", id, err)
	}
}

func TestNext_RespectsDependencies(t *testing.T) {
	t.Parallel()
	svc, _ := newStoryService(t)
	ctx := context.Background()

	seed(t, svc, "1.1", state.StatusComplete)
	seed(t, svc, "1.2", state.StatusPending)
	seed(t, svc, "1.3", state.StatusPending)
	_ = svc.Dependencies.Add(ctx, "1.3", "1.2") // 1.3 blocked by 1.2

	out, err := svc.Next(ctx, 10)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if len(out) != 1 || out[0].StoryID != "1.2" {
		t.Fatalf("Next = %+v, want [1.2] (1.3 blocked by pending 1.2)", out)
	}
}

// TestStoryNext_ScopeFilters seeds pending stories across 3 epics; --scope 2
// must return only Epic 2's candidates, none from 1.* or 3.*. Issue #35.
func TestStoryNext_ScopeFilters(t *testing.T) {
	t.Parallel()
	svc, _ := newStoryService(t)
	ctx := context.Background()

	// Three epics, two stories each — all pending, no deps, no affects.
	for _, id := range []string{"1.1", "1.2", "2.1", "2.2", "3.1", "3.2"} {
		seed(t, svc, id, state.StatusPending)
	}

	out, err := svc.NextWithScope(ctx, 10, "2")
	if err != nil {
		t.Fatalf("NextWithScope: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("NextWithScope --scope 2 len = %d, want 2 (2.1 + 2.2)", len(out))
	}
	for _, n := range out {
		if n.StoryID != "2.1" && n.StoryID != "2.2" {
			t.Fatalf("scope 2 returned out-of-scope story %q (full: %+v)", n.StoryID, out)
		}
	}
}

// TestStoryNext_ScopeEmpty — --scope 99 (no matching epic) is NOT an error;
// it returns an empty batch + exit 0 (the semantic equivalent of "nothing
// dispatchable right now in this scope"). Issue #35.
func TestStoryNext_ScopeEmpty(t *testing.T) {
	t.Parallel()
	svc, _ := newStoryService(t)
	ctx := context.Background()

	seed(t, svc, "1.1", state.StatusPending)
	seed(t, svc, "2.1", state.StatusPending)

	out, err := svc.NextWithScope(ctx, 10, "99")
	if err != nil {
		t.Fatalf("NextWithScope --scope 99 (no match) errored: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("NextWithScope --scope 99 = %+v, want empty batch", out)
	}
}

// TestStoryNext_ScopeWithMaxParallel — scope + max compose: --scope 2
// returns only 2.* candidates, AND the max cap clamps within that subset.
// Verifies the filter is applied BEFORE the cap (else max would be filled
// with 1.* and then dropped, breaking the contract).
func TestStoryNext_ScopeWithMaxParallel(t *testing.T) {
	t.Parallel()
	svc, _ := newStoryService(t)
	ctx := context.Background()

	// Epic 1 has 1 story; Epic 2 has 3 stories. Cap to 2 → expect 2 from Epic 2.
	seed(t, svc, "1.1", state.StatusPending)
	seed(t, svc, "2.1", state.StatusPending)
	seed(t, svc, "2.2", state.StatusPending)
	seed(t, svc, "2.3", state.StatusPending)

	out, err := svc.NextWithScope(ctx, 2, "2")
	if err != nil {
		t.Fatalf("NextWithScope: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("NextWithScope --scope 2 --max 2 len = %d, want 2", len(out))
	}
	for _, n := range out {
		if !(n.StoryID == "2.1" || n.StoryID == "2.2" || n.StoryID == "2.3") {
			t.Fatalf("scope 2 returned out-of-scope story %q (full: %+v)", n.StoryID, out)
		}
	}
}

// TestStoryNext_ScopeOffByOne — --scope 1 must NOT match 10.* (prefix+dot
// rule, mirroring sprint plan --scope). Guards against the "1" prefix
// accidentally matching "10.1" via naive HasPrefix.
func TestStoryNext_ScopeOffByOne(t *testing.T) {
	t.Parallel()
	svc, _ := newStoryService(t)
	ctx := context.Background()

	seed(t, svc, "1.1", state.StatusPending)
	seed(t, svc, "10.1", state.StatusPending)

	out, err := svc.NextWithScope(ctx, 10, "1")
	if err != nil {
		t.Fatalf("NextWithScope: %v", err)
	}
	if len(out) != 1 || out[0].StoryID != "1.1" {
		t.Fatalf("NextWithScope --scope 1 = %+v, want only 1.1 (10.1 must NOT match)", out)
	}
}

// TestStoryNext_EmptyScopeEquivalentToNext — sanity: an empty scope is a
// no-op filter (verifies backward compatibility with existing svc.Next).
func TestStoryNext_EmptyScopeEquivalentToNext(t *testing.T) {
	t.Parallel()
	svc, _ := newStoryService(t)
	ctx := context.Background()

	seed(t, svc, "1.1", state.StatusPending)
	seed(t, svc, "2.1", state.StatusPending)

	a, err := svc.Next(ctx, 10)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	b, err := svc.NextWithScope(ctx, 10, "")
	if err != nil {
		t.Fatalf("NextWithScope: %v", err)
	}
	if len(a) != len(b) {
		t.Fatalf("Next len %d != NextWithScope(\"\") len %d", len(a), len(b))
	}
}

func TestNext_FileOverlapPreventsParallelism(t *testing.T) {
	t.Parallel()
	svc, _ := newStoryService(t)
	ctx := context.Background()

	seed(t, svc, "a", state.StatusPending)
	seed(t, svc, "b", state.StatusPending)
	seed(t, svc, "c", state.StatusPending)
	_ = svc.Affects.Add(ctx, "a", "src/identity/")
	_ = svc.Affects.Add(ctx, "b", "src/identity/") // overlaps with a
	_ = svc.Affects.Add(ctx, "c", "src/billing/")  // disjoint

	out, _ := svc.Next(ctx, 10)
	if len(out) != 2 {
		t.Fatalf("Next len = %d, want 2 (a + c; b drops on overlap with a)", len(out))
	}
	for _, n := range out {
		if n.StoryID == "b" {
			t.Fatalf("b should have been dropped on overlap with a: %+v", out)
		}
	}
}

func TestEvaluateCheckpoint_ComplexityTriggers(t *testing.T) {
	t.Parallel()
	svc, db := newStoryService(t)
	ctx := context.Background()

	if err := svc.Stories.Insert(ctx, state.Story{
		ID: "big", File: "big.md", Title: "Big",
		Status: state.StatusComplete, Complexity: state.ComplexityHigh,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	res, err := svc.EvaluateCheckpoint(ctx, "big")
	if err != nil {
		t.Fatalf("EvaluateCheckpoint: %v", err)
	}
	if !res.Fired || res.TriggerKind != state.CheckpointComplexity {
		t.Fatalf("checkpoint result = %+v, want complexity-fired", res)
	}
	_ = db
}

func TestEvaluateCheckpoint_CountTriggersAtThreshold(t *testing.T) {
	t.Parallel()
	svc, _ := newStoryService(t)
	ctx := context.Background()

	_ = svc.Config.Set(ctx, "checkpoint.count_threshold", "2")

	for _, id := range []string{"x.1", "x.2"} {
		seed(t, svc, id, state.StatusComplete)
	}
	res, err := svc.EvaluateCheckpoint(ctx, "x.2")
	if err != nil {
		t.Fatalf("EvaluateCheckpoint: %v", err)
	}
	if !res.Fired || res.TriggerKind != state.CheckpointCount {
		t.Fatalf("res = %+v, want count-fired", res)
	}
}

func TestEvaluateCheckpoint_DoesNotFireBelowThreshold(t *testing.T) {
	t.Parallel()
	svc, _ := newStoryService(t)
	ctx := context.Background()
	_ = svc.Config.Set(ctx, "checkpoint.count_threshold", "5")
	seed(t, svc, "small", state.StatusComplete)

	res, _ := svc.EvaluateCheckpoint(ctx, "small")
	if res.Fired {
		t.Fatalf("checkpoint fired below threshold: %+v", res)
	}
}
