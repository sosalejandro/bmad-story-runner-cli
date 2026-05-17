package env_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	appenv "github.com/sosalejandro/bmad-story-runner-cli/application/env"
	"github.com/sosalejandro/bmad-story-runner-cli/domain/state"
	"github.com/sosalejandro/bmad-story-runner-cli/infrastructure/state/sqlite"
)

// fixedClock returns a constant time for deterministic threshold tests.
func fixedClock(t time.Time) func() time.Time {
	return func() time.Time { return t }
}

func newSweeperFixture(t *testing.T) (*sqlite.DB, *appenv.Sweeper) {
	t.Helper()
	db, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "bmad-state.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, appenv.NewSweeper(
		sqlite.NewEnvsStore(db),
		sqlite.NewWorktreesStore(db),
		sqlite.NewDispatchesStore(db),
		sqlite.NewConfigStore(db),
	)
}

// seedEnvForSweeper inserts a story + reserves an env so the sweeper has
// something to probe. Returns the env's reserved StoryID for downstream use.
func seedEnvForSweeper(t *testing.T, db *sqlite.DB, id string) {
	t.Helper()
	ctx := context.Background()
	if err := sqlite.NewStoriesStore(db).Insert(ctx, state.Story{
		ID: id, File: id + ".md", Title: id,
		Status: state.StatusPending, Complexity: state.ComplexityMedium,
	}); err != nil {
		t.Fatalf("seed story %q: %v", id, err)
	}
	if err := sqlite.NewEnvsStore(db).Reserve(ctx, state.EnvAllocation{
		StoryID: id, PGPort: 7600, RedisPort: 7601, DBName: "story_" + id,
	}); err != nil {
		t.Fatalf("reserve env %q: %v", id, err)
	}
}

func TestSweeper_KeepsFreshEnvWithNoDispatchOrWorktree(t *testing.T) {
	t.Parallel()
	db, sweeper := newSweeperFixture(t)
	ctx := context.Background()

	seedEnvForSweeper(t, db, "fresh")
	// Pin clock to 30s after the row was created (which was ~now).
	sweeper.Now = fixedClock(time.Now().Add(30 * time.Second))

	res, err := sweeper.Sweep(ctx, 60) // 60-min threshold
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if len(res.Swept) != 0 || len(res.Kept) != 1 || res.Kept[0] != "fresh" {
		t.Fatalf("fresh env wrongly swept: %+v", res)
	}
}

func TestSweeper_SweepsOldEnvWithNoActivity(t *testing.T) {
	t.Parallel()
	db, sweeper := newSweeperFixture(t)
	ctx := context.Background()

	seedEnvForSweeper(t, db, "stale")
	// Pin clock 4 hours into the future relative to the row's CreatedAt.
	sweeper.Now = fixedClock(time.Now().Add(4 * time.Hour))

	res, err := sweeper.Sweep(ctx, 120) // 120-min threshold
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if len(res.Swept) != 1 || res.Swept[0] != "stale" {
		t.Fatalf("stale env not swept: %+v", res)
	}
	a, err := sqlite.NewEnvsStore(db).Get(ctx, "stale")
	if err != nil {
		t.Fatalf("post-sweep Get: %v", err)
	}
	if a.ReclaimedAt == nil || a.ReclaimReason == nil || *a.ReclaimReason != "stale" {
		t.Fatalf("env_allocations not marked stale: %+v", a)
	}
}

func TestSweeper_KeepsOldEnvWithRecentDispatch(t *testing.T) {
	t.Parallel()
	db, sweeper := newSweeperFixture(t)
	ctx := context.Background()

	seedEnvForSweeper(t, db, "busy")

	// Record a dispatch returned 30 minutes ago relative to "now+4h".
	now := time.Now()
	returned := now.Add(3*time.Hour + 30*time.Minute) // 30min before cutoff
	if _, err := sqlite.NewDispatchesStore(db).Insert(ctx, state.Dispatch{
		StoryID: "busy", Stage: state.StageImplement, AgentRole: "tdd-implementer",
		AttemptNo: 1, Status: state.DispatchOK, DurationMS: 1000,
		ReturnedAt: &returned,
	}); err != nil {
		t.Fatalf("seed dispatch: %v", err)
	}

	// Pin clock 4h after creation; threshold 60min → cutoff = now+3h.
	// Env CreatedAt (~now) is BEFORE cutoff (probe 0 false), but the
	// dispatch returned at now+3h30m is AFTER cutoff (probe 1 catches it).
	sweeper.Now = fixedClock(now.Add(4 * time.Hour))

	res, err := sweeper.Sweep(ctx, 60)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if len(res.Kept) != 1 || res.Kept[0] != "busy" {
		t.Fatalf("busy env wrongly swept: %+v", res)
	}
}
