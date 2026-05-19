package sqlite_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sosalejandro/bmad-story-runner-cli/domain/state"
	"github.com/sosalejandro/bmad-story-runner-cli/infrastructure/state/sqlite"
)

// seedStory inserts a minimal valid story so FK-bearing adapters have a target.
func seedStory(t *testing.T, db *sqlite.DB, id string) {
	t.Helper()
	stores := sqlite.NewStoriesStore(db)
	st := state.Story{
		ID:         id,
		File:       id + ".md",
		Title:      "Story " + id,
		Status:     state.StatusPending,
		Complexity: state.ComplexityMedium,
	}
	if err := stores.Insert(context.Background(), st); err != nil {
		t.Fatalf("seed story %q: %v", id, err)
	}
}

// ---------- Stories ----------

func TestStories_InsertGetRoundTrip(t *testing.T) {
	t.Parallel()
	db := newTempDB(t)
	store := sqlite.NewStoriesStore(db)
	ctx := context.Background()

	stage := state.StageImplement
	hydrated := "_bmad/stories/4.1.md"
	group := 1
	in := state.Story{
		ID:              "4.1",
		File:            "4.1-identity.md",
		Title:           "Identity Aggregates",
		Status:          state.StatusInProgress,
		CurrentStage:    &stage,
		ParallelGroup:   &group,
		HydratedFile:    &hydrated,
		ResourceBudget:  &state.ResourceBudget{RamMB: 800, CPUCores: 0.6},
		RequiresAndroid: false,
		Complexity:      state.ComplexityHigh,
	}
	if err := store.Insert(ctx, in); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	got, err := store.Get(ctx, "4.1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Title != in.Title || got.Status != in.Status || got.Complexity != in.Complexity {
		t.Fatalf("round-trip mismatch: got %+v want %+v", got, in)
	}
	if got.CurrentStage == nil || *got.CurrentStage != stage {
		t.Fatalf("CurrentStage = %v, want %v", got.CurrentStage, stage)
	}
	if got.ResourceBudget == nil || got.ResourceBudget.RamMB != 800 {
		t.Fatalf("ResourceBudget round-trip lost data: %+v", got.ResourceBudget)
	}
}

func TestStories_GetUnknownReturnsErrNotFound(t *testing.T) {
	t.Parallel()
	store := sqlite.NewStoriesStore(newTempDB(t))
	if _, err := store.Get(context.Background(), "nope"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("Get unknown err = %v, want ErrNotFound", err)
	}
}

func TestStories_ListByStatus(t *testing.T) {
	t.Parallel()
	db := newTempDB(t)
	store := sqlite.NewStoriesStore(db)
	ctx := context.Background()

	for _, id := range []string{"1.1", "1.2", "1.3"} {
		seedStory(t, db, id)
	}
	if err := store.SetStatus(ctx, "1.2", state.StatusComplete); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}

	want := state.StatusComplete
	got, err := store.List(ctx, state.StoryFilter{Status: &want})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || got[0].ID != "1.2" {
		t.Fatalf("List by status = %+v, want [1.2]", got)
	}
}

func TestStories_SetComplete(t *testing.T) {
	t.Parallel()
	db := newTempDB(t)
	store := sqlite.NewStoriesStore(db)
	ctx := context.Background()

	seedStory(t, db, "2.1")
	if err := store.SetComplete(ctx, "2.1", "abc123", "https://github.com/x/y/pull/1"); err != nil {
		t.Fatalf("SetComplete: %v", err)
	}
	got, _ := store.Get(ctx, "2.1")
	if got.Status != state.StatusComplete || !got.CIPassed {
		t.Fatalf("SetComplete state mismatch: %+v", got)
	}
	if got.CommitHash == nil || *got.CommitHash != "abc123" {
		t.Fatalf("CommitHash = %v, want abc123", got.CommitHash)
	}
}

// ---------- Story relations ----------

func TestStoryDependencies_AddAndQuery(t *testing.T) {
	t.Parallel()
	db := newTempDB(t)
	seedStory(t, db, "1.1")
	seedStory(t, db, "1.2")
	seedStory(t, db, "1.3")

	deps := sqlite.NewStoryDependenciesStore(db)
	ctx := context.Background()
	for _, dep := range []string{"1.1", "1.2"} {
		if err := deps.Add(ctx, "1.3", dep); err != nil {
			t.Fatalf("Add(%q): %v", dep, err)
		}
	}

	got, err := deps.Of(ctx, "1.3")
	if err != nil {
		t.Fatalf("Of: %v", err)
	}
	if len(got) != 2 || got[0] != "1.1" || got[1] != "1.2" {
		t.Fatalf("Of(1.3) = %v, want [1.1 1.2]", got)
	}

	dependents, _ := deps.DependentsOf(ctx, "1.1")
	if len(dependents) != 1 || dependents[0] != "1.3" {
		t.Fatalf("DependentsOf(1.1) = %v, want [1.3]", dependents)
	}
}

func TestStoryAffects_StoriesAffecting(t *testing.T) {
	t.Parallel()
	db := newTempDB(t)
	seedStory(t, db, "a.1")
	seedStory(t, db, "a.2")
	store := sqlite.NewStoryAffectsStore(db)
	ctx := context.Background()
	_ = store.Add(ctx, "a.1", "src/identity/")
	_ = store.Add(ctx, "a.2", "src/identity/")
	_ = store.Add(ctx, "a.2", "src/billing/")

	got, _ := store.StoriesAffecting(ctx, "src/identity/")
	if len(got) != 2 {
		t.Fatalf("StoriesAffecting identity = %v, want 2 results", got)
	}
}

func TestStoryConcerns_Append(t *testing.T) {
	t.Parallel()
	db := newTempDB(t)
	seedStory(t, db, "c.1")
	store := sqlite.NewStoryConcernsStore(db)
	ctx := context.Background()

	_ = store.Add(ctx, "c.1", "test-reviewer", `{"finding":"flaky"}`)
	_ = store.Add(ctx, "c.1", "code-reviewer", `{"finding":"god-class"}`)
	got, err := store.Of(ctx, "c.1")
	if err != nil {
		t.Fatalf("Of: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("Of(c.1) = %d concerns, want 2", len(got))
	}
}

func TestStoryRetryCounts_Increments(t *testing.T) {
	t.Parallel()
	db := newTempDB(t)
	seedStory(t, db, "r.1")
	store := sqlite.NewStoryRetryCountsStore(db)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		_ = store.IncTDD(ctx, "r.1")
	}
	_ = store.IncReview(ctx, "r.1")

	rc, err := store.Get(ctx, "r.1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if rc.TDDCycles != 3 || rc.ReviewIterations != 1 {
		t.Fatalf("counts = %+v, want TDD=3 Review=1", rc)
	}
}

// ---------- Dispatches ----------

func TestDispatches_InsertAndRollup(t *testing.T) {
	t.Parallel()
	db := newTempDB(t)
	seedStory(t, db, "d.1")
	store := sqlite.NewDispatchesStore(db)
	ctx := context.Background()

	for i := 1; i <= 3; i++ {
		_, err := store.Insert(ctx, state.Dispatch{
			StoryID:   "d.1",
			Stage:     state.StageImplement,
			AgentRole: "tdd-implementer",
			AttemptNo: i,
			Status:    state.DispatchOK,
			Tokens:    state.TokenCounts{Input: 1000, Output: 200, CacheRead: 500, CacheCreate: 100},
			DurationMS: 12_000,
		})
		if err != nil {
			t.Fatalf("Insert %d: %v", i, err)
		}
	}

	rollup, err := store.TokenRollupForStory(ctx, "d.1")
	if err != nil {
		t.Fatalf("rollup: %v", err)
	}
	if rollup.Input != 3000 || rollup.Output != 600 {
		t.Fatalf("rollup = %+v, want input=3000 output=600", rollup)
	}

	last, _ := store.LastForStory(ctx, "d.1", state.StageImplement)
	if last == nil || last.AttemptNo != 3 {
		t.Fatalf("LastForStory attempt = %v, want 3", last)
	}
}

// ---------- Envs ----------

func TestEnvs_ReserveActiveRelease(t *testing.T) {
	t.Parallel()
	db := newTempDB(t)
	seedStory(t, db, "e.1")
	store := sqlite.NewEnvsStore(db)
	ctx := context.Background()

	otel := 7613
	if err := store.Reserve(ctx, state.EnvAllocation{
		StoryID:      "e.1",
		PGPort:       7611,
		RedisPort:    7612,
		OtelPort:     &otel,
		DBName:       "story_e_1",
		ContainerIDs: []string{"c1", "c2"},
	}); err != nil {
		t.Fatalf("Reserve: %v", err)
	}

	active, _ := store.Active(ctx)
	if len(active) != 1 || len(active[0].ContainerIDs) != 2 {
		t.Fatalf("Active = %+v", active)
	}

	ports, _ := store.InUsePorts(ctx)
	if len(ports) != 3 {
		t.Fatalf("InUsePorts = %v, want 3", ports)
	}

	if err := store.Release(ctx, "e.1", "completed"); err != nil {
		t.Fatalf("Release: %v", err)
	}

	active, _ = store.Active(ctx)
	if len(active) != 0 {
		t.Fatalf("after release Active = %+v, want empty", active)
	}
}

// ---------- Worktrees ----------

func TestWorktrees_CreateTouchDelete(t *testing.T) {
	t.Parallel()
	db := newTempDB(t)
	seedStory(t, db, "w.1")
	store := sqlite.NewWorktreesStore(db)
	ctx := context.Background()

	if err := store.Create(ctx, state.Worktree{
		StoryID: "w.1", Path: ".worktrees/w.1", BranchName: "story/w.1",
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	now := time.Now().UTC().Round(time.Second)
	if err := store.TouchActivity(ctx, "w.1", now); err != nil {
		t.Fatalf("TouchActivity: %v", err)
	}

	got, _ := store.Get(ctx, "w.1")
	if got.LastActivityAt == nil {
		t.Fatalf("LastActivityAt nil after Touch")
	}

	_ = store.Delete(ctx, "w.1")
	if _, err := store.Get(ctx, "w.1"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("Get after Delete err = %v, want ErrNotFound", err)
	}
}

// ---------- Batches ----------

func TestBatches_InsertAndNextPlanned(t *testing.T) {
	t.Parallel()
	db := newTempDB(t)
	seedStory(t, db, "b.1")
	seedStory(t, db, "b.2")
	store := sqlite.NewBatchesStore(db)
	ctx := context.Background()

	id, err := store.Insert(ctx, state.Batch{
		SequenceNo: 1,
		Status:     state.BatchPlanned,
		StoryIDs:   []string{"b.1", "b.2"},
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}

	next, _ := store.NextPlanned(ctx)
	if next == nil || next.ID != id || len(next.StoryIDs) != 2 {
		t.Fatalf("NextPlanned = %+v, want batch %d", next, id)
	}

	if err := store.MarkStarted(ctx, id, time.Now().UTC()); err != nil {
		t.Fatalf("MarkStarted: %v", err)
	}
	next, _ = store.NextPlanned(ctx)
	if next != nil {
		t.Fatalf("NextPlanned after Start should be nil, got %+v", next)
	}
}

// ---------- Checkpoints ----------

func TestCheckpoints_FireAndDecide(t *testing.T) {
	t.Parallel()
	db := newTempDB(t)
	store := sqlite.NewCheckpointsStore(db)
	ctx := context.Background()

	detail := "complexity:4.5"
	id, err := store.Fire(ctx, state.Checkpoint{
		TriggerKind:      state.CheckpointComplexity,
		TriggerDetail:    &detail,
		StoriesSinceLast: 3,
		SummaryJSON:      `{"foo":"bar"}`,
	})
	if err != nil {
		t.Fatalf("Fire: %v", err)
	}

	unresolved, _ := store.Unresolved(ctx)
	if unresolved == nil || unresolved.ID != id {
		t.Fatalf("Unresolved = %+v, want id %d", unresolved, id)
	}

	if err := store.Decide(ctx, id, state.DecisionContinue, time.Now().UTC()); err != nil {
		t.Fatalf("Decide: %v", err)
	}

	unresolved, _ = store.Unresolved(ctx)
	if unresolved != nil {
		t.Fatalf("after Decide, Unresolved = %+v, want nil", unresolved)
	}
}

// ---------- Depguard ----------

func TestDepguard_FlipAndHistory(t *testing.T) {
	t.Parallel()
	db := newTempDB(t)
	store := sqlite.NewDepguardStore(db)
	ctx := context.Background()

	if err := store.Flip(ctx, "no_infra_in_domain", state.DepguardWarn, "initial"); err != nil {
		t.Fatalf("Flip warn: %v", err)
	}
	if err := store.Flip(ctx, "no_infra_in_domain", state.DepguardError, "tighten for prod"); err != nil {
		t.Fatalf("Flip error: %v", err)
	}

	current, err := store.Get(ctx, "no_infra_in_domain")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if current.State != state.DepguardError {
		t.Fatalf("current.State = %v, want error", current.State)
	}

	hist, _ := store.History(ctx, "no_infra_in_domain")
	if len(hist) != 2 {
		t.Fatalf("History len = %d, want 2", len(hist))
	}
	if hist[1].From != state.DepguardWarn || hist[1].To != state.DepguardError {
		t.Fatalf("History[1] transition = %v→%v, want warn→error", hist[1].From, hist[1].To)
	}
}

// ---------- Stories.ReapStaleClaims (issue #21 gap 3) ----------

func TestStories_ReapStaleClaims_ReapsOnlyStaleNonComplete(t *testing.T) {
	t.Parallel()
	db := newTempDB(t)
	store := sqlite.NewStoriesStore(db)
	ctx := context.Background()

	// Three rows: a stale non-complete claim (reap target), a fresh claim
	// (preserve), and a stale claim on a complete story (preserve audit).
	seedStory(t, db, "stale-pending")
	seedStory(t, db, "fresh-pending")
	seedStory(t, db, "stale-complete")

	// Fresh claim on all three.
	if _, err := store.ClaimUnclaimedPending(ctx, []string{"stale-pending", "fresh-pending", "stale-complete"}, 10, "orchestrator"); err != nil {
		t.Fatalf("claim 3: %v", err)
	}
	// Backdate the stale ones via the test helper (lives in
	// `internal_test.go` inside package sqlite for access to sqlDB()).
	sqlite.BackdateClaimForTest(t, db, "stale-pending", 1000)
	sqlite.BackdateClaimForTest(t, db, "stale-complete", 1000)
	if err := store.SetStatus(ctx, "stale-complete", state.StatusComplete); err != nil {
		t.Fatalf("set status complete: %v", err)
	}

	reaped, err := store.ReapStaleClaims(ctx, 600)
	if err != nil {
		t.Fatalf("ReapStaleClaims: %v", err)
	}
	if len(reaped) != 1 || reaped[0] != "stale-pending" {
		t.Fatalf("reaped = %v, want [stale-pending]", reaped)
	}

	sp, _ := store.Get(ctx, "stale-pending")
	if sp.ClaimedAt != nil {
		t.Errorf("stale-pending claim not cleared: %v", sp.ClaimedAt)
	}
	fp, _ := store.Get(ctx, "fresh-pending")
	if fp.ClaimedAt == nil {
		t.Errorf("fresh-pending claim cleared — should be preserved")
	}
	sc, _ := store.Get(ctx, "stale-complete")
	if sc.ClaimedAt == nil {
		t.Errorf("stale-complete claim cleared — should be preserved (audit)")
	}
}

func TestStories_ReapStaleClaims_TTLZeroDisablesReaper(t *testing.T) {
	t.Parallel()
	db := newTempDB(t)
	store := sqlite.NewStoriesStore(db)
	ctx := context.Background()
	seedStory(t, db, "x")
	if _, err := store.ClaimUnclaimedPending(ctx, []string{"x"}, 1, "orchestrator"); err != nil {
		t.Fatalf("claim: %v", err)
	}
	sqlite.BackdateClaimForTest(t, db, "x", 10000)

	reaped, err := store.ReapStaleClaims(ctx, 0)
	if err != nil {
		t.Fatalf("reap: %v", err)
	}
	if len(reaped) != 0 {
		t.Errorf("ttl=0 should disable reaper, got reaped=%v", reaped)
	}
	got, _ := store.Get(ctx, "x")
	if got.ClaimedAt == nil {
		t.Errorf("ttl=0 reaped a claim it shouldn't have")
	}
}

// ---------- Compile-time interface checks ----------

var (
	_ state.Stories          = (*sqlite.StoriesStore)(nil)
	_ state.StoryDependencies = (*sqlite.StoryDependenciesStore)(nil)
	_ state.StoryAffects     = (*sqlite.StoryAffectsStore)(nil)
	_ state.StoryConcerns    = (*sqlite.StoryConcernsStore)(nil)
	_ state.StoryRetryCounts = (*sqlite.StoryRetryCountsStore)(nil)
	_ state.Dispatches       = (*sqlite.DispatchesStore)(nil)
	_ state.Envs             = (*sqlite.EnvsStore)(nil)
	_ state.Worktrees        = (*sqlite.WorktreesStore)(nil)
	_ state.Batches          = (*sqlite.BatchesStore)(nil)
	_ state.Checkpoints      = (*sqlite.CheckpointsStore)(nil)
	_ state.Depguard         = (*sqlite.DepguardStore)(nil)
)
