package cmd

import (
	"context"
	"errors"
	"math"
	"path/filepath"
	"testing"

	appstate "github.com/sosalejandro/bmad-story-runner-cli/application/state"
	"github.com/sosalejandro/bmad-story-runner-cli/domain/state"
	"github.com/sosalejandro/bmad-story-runner-cli/infrastructure/state/sqlite"
)

// newStoryServiceForTest assembles a StoryService backed by sqlite stores —
// used by tests that exercise cmd-layer helpers (effectiveClaimTTL,
// stale-claim reaping at the cmd boundary).
func newStoryServiceForTest(db *sqlite.DB) *appstate.StoryService {
	return &appstate.StoryService{
		Stories:      sqlite.NewStoriesStore(db),
		Dependencies: sqlite.NewStoryDependenciesStore(db),
		Affects:      sqlite.NewStoryAffectsStore(db),
		Concerns:     sqlite.NewStoryConcernsStore(db),
		RetryCounts:  sqlite.NewStoryRetryCountsStore(db),
		Config:       sqlite.NewConfigStore(db),
		Checkpoints:  sqlite.NewCheckpointsStore(db),
	}
}

// Issue #21 gap 2: `bmad dispatch record --hydrated-file` must (a) write
// stories.hydrated_file when stage=hydrate AND status=ok, (b) reject
// non-hydrate stages or non-ok statuses with a clear error, (c) refuse to
// clobber an existing non-empty value unless --overwrite-hydrated-file is
// passed. The applyHydratedFile helper is exactly the validation seam, so
// testing it covers the cmd surface without spinning up the full
// cobra-RunE harness.
func TestApplyHydratedFile_WritesOnHydrateOK(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "bmad-state.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	stories := sqlite.NewStoriesStore(db)
	if err := stories.Insert(ctx, state.Story{
		ID: "2.1", Title: "Cross-cutting", Status: state.StatusPending,
		Complexity: state.ComplexityMedium, StoryType: state.StoryTypeCode,
	}); err != nil {
		t.Fatalf("seed story: %v", err)
	}

	const path = "/tmp/2.1-hydrated.md"
	if err := applyHydratedFile(ctx, stories, "2.1", state.StageHydrate, state.DispatchOK, path, false); err != nil {
		t.Fatalf("applyHydratedFile (clean): %v", err)
	}

	st, _ := stories.Get(ctx, "2.1")
	if st.HydratedFile == nil || *st.HydratedFile != path {
		t.Fatalf("hydrated_file = %v, want %q", st.HydratedFile, path)
	}
}

// Re-running with the SAME path is a no-op no-error (idempotent replay).
// Re-running with a DIFFERENT path errors unless --overwrite is passed.
func TestApplyHydratedFile_RefusesOverwriteByDefault(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "bmad-state.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	stories := sqlite.NewStoriesStore(db)
	if err := stories.Insert(ctx, state.Story{
		ID: "2.1", Title: "x", Status: state.StatusPending,
		Complexity: state.ComplexityMedium, StoryType: state.StoryTypeCode,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	const first = "/tmp/first.md"
	const second = "/tmp/second.md"

	if err := applyHydratedFile(ctx, stories, "2.1", state.StageHydrate, state.DispatchOK, first, false); err != nil {
		t.Fatalf("first write: %v", err)
	}
	// Same path → no-error (the SetHydratedFile adapter sees existing ==
	// new and skips the overwrite check).
	if err := applyHydratedFile(ctx, stories, "2.1", state.StageHydrate, state.DispatchOK, first, false); err != nil {
		t.Errorf("same-path replay should be idempotent, got error: %v", err)
	}
	// Different path without overwrite → friendly error mentioning the flag.
	err = applyHydratedFile(ctx, stories, "2.1", state.StageHydrate, state.DispatchOK, second, false)
	if err == nil {
		t.Fatal("different-path-without-overwrite: expected error, got nil")
	}
	// With overwrite=true → succeeds and stores the new path.
	if err := applyHydratedFile(ctx, stories, "2.1", state.StageHydrate, state.DispatchOK, second, true); err != nil {
		t.Fatalf("with overwrite: %v", err)
	}
	st, _ := stories.Get(ctx, "2.1")
	if st.HydratedFile == nil || *st.HydratedFile != second {
		t.Errorf("after overwrite: hydrated_file = %v, want %q", st.HydratedFile, second)
	}
}

// Stage / status guards: the flag is only honored on the success path of
// a hydrate dispatch. Other combinations must fail loudly so an
// orchestrator script bug surfaces at the boundary.
func TestApplyHydratedFile_RejectsNonHydrateOrNonOK(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "bmad-state.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	stories := sqlite.NewStoriesStore(db)
	if err := stories.Insert(ctx, state.Story{
		ID: "2.1", Title: "x", Status: state.StatusPending,
		Complexity: state.ComplexityMedium, StoryType: state.StoryTypeCode,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	cases := []struct {
		name   string
		stage  state.Stage
		status state.DispatchStatus
	}{
		{"atdd-ok", state.StageATDD, state.DispatchOK},
		{"implement-ok", state.StageImplement, state.DispatchOK},
		{"hydrate-blocked", state.StageHydrate, state.DispatchBlocked},
		{"hydrate-errored", state.StageHydrate, state.DispatchErrored},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := applyHydratedFile(ctx, stories, "2.1", tc.stage, tc.status, "/tmp/x.md", false)
			if err == nil {
				t.Fatalf("expected error for stage=%s status=%s, got nil", tc.stage, tc.status)
			}
		})
	}
}

// Issue #21 gap 3 (a): a successful dispatch must release the story's
// claim so the next orchestrator iteration can advance the story (or
// move on). The pre-fix state was: hydrate-success dispatch recorded ok,
// but `claimed_at` stayed populated, so `bmad story next` continued to
// skip the story until an operator ran raw SQL.
func TestReleaseClaimOnOK_ClearsClaimOnSuccess(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "bmad-state.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	stories := sqlite.NewStoriesStore(db)
	if err := stories.Insert(ctx, state.Story{
		ID: "2.1", Title: "x", Status: state.StatusPending,
		Complexity: state.ComplexityMedium, StoryType: state.StoryTypeCode,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// Simulate the orchestrator claim path.
	if _, err := stories.ClaimUnclaimedPending(ctx, []string{"2.1"}, 1, "orchestrator"); err != nil {
		t.Fatalf("claim: %v", err)
	}
	pre, _ := stories.Get(ctx, "2.1")
	if pre.ClaimedAt == nil {
		t.Fatal("setup precondition: 2.1 should be claimed")
	}

	if err := releaseClaimOnOK(ctx, stories, "2.1", state.DispatchOK); err != nil {
		t.Fatalf("releaseClaimOnOK: %v", err)
	}

	post, _ := stories.Get(ctx, "2.1")
	if post.ClaimedAt != nil {
		t.Errorf("claim not cleared on ok: %v", post.ClaimedAt)
	}
}

// Inverse: blocked / errored dispatches MUST NOT release the claim. The
// orchestrator might want to retry (still holds the claim) or surface to
// the user for a decision. Auto-releasing would race a follow-up retry.
func TestReleaseClaimOnOK_PreservesClaimOnNonOK(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "bmad-state.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	stories := sqlite.NewStoriesStore(db)
	if err := stories.Insert(ctx, state.Story{
		ID: "z", Title: "x", Status: state.StatusPending,
		Complexity: state.ComplexityMedium, StoryType: state.StoryTypeCode,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := stories.ClaimUnclaimedPending(ctx, []string{"z"}, 1, "orchestrator"); err != nil {
		t.Fatalf("claim: %v", err)
	}

	for _, st := range []state.DispatchStatus{state.DispatchBlocked, state.DispatchErrored} {
		if err := releaseClaimOnOK(ctx, stories, "z", st); err != nil {
			t.Errorf("releaseClaimOnOK(%s) returned error: %v", st, err)
		}
		got, _ := stories.Get(ctx, "z")
		if got.ClaimedAt == nil {
			t.Errorf("status=%s wrongly cleared the claim", st)
		}
	}
}

// effectiveClaimTTL: config-driven knob with sensible fallback. Operators
// can tune via `bmad config set claim_ttl_seconds <n>`; a missing or
// malformed value falls back to DefaultClaimTTLSeconds; an explicit "0"
// disables the reaper.
func TestEffectiveClaimTTL_FallbackBehavior(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "bmad-state.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	svc := newStoryServiceForTest(db)

	// Unset → default.
	if got := effectiveClaimTTL(ctx, svc); got != DefaultClaimTTLSeconds {
		t.Errorf("unset: ttl = %d, want %d", got, DefaultClaimTTLSeconds)
	}
	// Bad value → default (resilient to admin typos).
	_ = svc.Config.Set(ctx, "claim_ttl_seconds", "not-a-number")
	if got := effectiveClaimTTL(ctx, svc); got != DefaultClaimTTLSeconds {
		t.Errorf("bad: ttl = %d, want %d", got, DefaultClaimTTLSeconds)
	}
	// Explicit "0" → disabled.
	_ = svc.Config.Set(ctx, "claim_ttl_seconds", "0")
	if got := effectiveClaimTTL(ctx, svc); got != 0 {
		t.Errorf("zero: ttl = %d, want 0", got)
	}
	// Explicit "300" → 300.
	_ = svc.Config.Set(ctx, "claim_ttl_seconds", "300")
	if got := effectiveClaimTTL(ctx, svc); got != 300 {
		t.Errorf("300: ttl = %d, want 300", got)
	}
}

// Defensive: the underlying SetHydratedFile must surface ErrAlreadySet so
// callers (here applyHydratedFile, but also any future verb that wants the
// same idempotency safety) can build a friendly message on top of it.
func TestStoriesSetHydratedFile_ErrAlreadySetSurface(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "bmad-state.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	stories := sqlite.NewStoriesStore(db)
	if err := stories.Insert(ctx, state.Story{
		ID: "z", Title: "x", Status: state.StatusPending,
		Complexity: state.ComplexityMedium, StoryType: state.StoryTypeCode,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := stories.SetHydratedFile(ctx, "z", "/a", false); err != nil {
		t.Fatalf("first: %v", err)
	}
	err = stories.SetHydratedFile(ctx, "z", "/b", false)
	if !errors.Is(err, state.ErrAlreadySet) {
		t.Fatalf("want ErrAlreadySet, got %v", err)
	}
}

// Issue #15: cache_hit_rate = cache_read / (input + cache_read) — derived from
// the 4-axis breakdown the subagent's <usage> block provides.
func TestCacheHitRate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   state.TokenCounts
		want float64
	}{
		// Zero denominator: no input AND no cache_read — undefined, so
		// return 0 rather than divide-by-zero panic / NaN.
		{"empty", state.TokenCounts{}, 0.0},
		{"only-output-and-create", state.TokenCounts{Output: 1000, CacheCreate: 500}, 0.0},

		{"all-fresh", state.TokenCounts{Input: 100, CacheRead: 0}, 0.0},
		{"perfect-cache", state.TokenCounts{Input: 0, CacheRead: 100}, 100.0},
		{"fifty-fifty", state.TokenCounts{Input: 50, CacheRead: 50}, 50.0},
		{"realistic", state.TokenCounts{Input: 1000, CacheRead: 9000, Output: 500, CacheCreate: 200}, 90.0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := cacheHitRate(tc.in)
			if math.Abs(got-tc.want) > 0.001 {
				t.Errorf("cacheHitRate(%+v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// Issue #15: --total-tokens must validate against the breakdown sum when both
// forms are provided. Asserted via the record command's flag-parsing path —
// here we just exercise the math the validator relies on.
func TestTokenBreakdownSumsMatchTotal(t *testing.T) {
	t.Parallel()
	t1 := state.TokenCounts{Input: 100, Output: 200, CacheRead: 300, CacheCreate: 50}
	got := t1.Input + t1.Output + t1.CacheRead + t1.CacheCreate
	if got != 650 {
		t.Fatalf("breakdown sum = %d, want 650", got)
	}
}

// Issue #15: reconcileTokenInputs decides what to persist when the caller
// mixes legacy --total-tokens with the new 4-axis breakdown flags. The four
// cases the L1 orchestrator actually exercises:
//
//   - new caller (breakdown only)        → use breakdown as-is
//   - legacy caller (total only)         → bucket total into input
//   - both, sums agree                   → use breakdown (total is redundant)
//   - both, sums disagree                → error (the validator caught a bug)
func TestReconcileTokenInputs(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		total     bool
		breakdown bool
		tot       int64
		i, o, cr, cc int64
		want      state.TokenCounts
		wantErr   bool
	}{
		{
			name:      "breakdown-only",
			breakdown: true,
			i: 100, o: 200, cr: 300, cc: 50,
			want: state.TokenCounts{Input: 100, Output: 200, CacheRead: 300, CacheCreate: 50},
		},
		{
			name:  "total-only-buckets-to-input",
			total: true,
			tot:   650,
			want:  state.TokenCounts{Input: 650},
		},
		{
			name:      "both-agree",
			total:     true,
			breakdown: true,
			tot:       650,
			i: 100, o: 200, cr: 300, cc: 50,
			want: state.TokenCounts{Input: 100, Output: 200, CacheRead: 300, CacheCreate: 50},
		},
		{
			name:      "both-disagree-errors",
			total:     true,
			breakdown: true,
			tot:       9999,
			i: 100, o: 200, cr: 300, cc: 50,
			wantErr: true,
		},
		{
			name: "neither-zeroes",
			want: state.TokenCounts{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := reconcileTokenInputs(tc.total, tc.breakdown, tc.tot, tc.i, tc.o, tc.cr, tc.cc)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil; got=%+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}
