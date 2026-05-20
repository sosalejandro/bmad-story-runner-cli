package sqlite_test

// Tests for CacheHitRate sentinel-detection logic (issue #42, Path 3).
//
// L3 agents can't introspect their own API usage block, so TOKEN_BREAKDOWN
// emits input=output=cache_read=cache_create=0 for every dispatch —
// making an unguarded aggregate falsely report "0%" cache. These tests
// assert that all-zero rows are treated as "value unknown, not zero."

import (
	"context"
	"testing"

	"github.com/sosalejandro/bmad-story-runner-cli/domain/state"
	"github.com/sosalejandro/bmad-story-runner-cli/infrastructure/state/sqlite"
)

// TestCacheHitRate_UnknownWhenAllBreakdownsZero validates issue #42 path 3:
// when every dispatch row for a story has input=output=cache_read=cache_create=0,
// the aggregate cache-hit-rate query reports "unknown" (zero rows counted),
// not "0%" (which would falsely imply we measured 0% cache).
func TestCacheHitRate_UnknownWhenAllBreakdownsZero(t *testing.T) {
	t.Parallel()
	db := newTempDB(t)
	seedStory(t, db, "z.1")
	store := sqlite.NewDispatchesStore(db)
	ctx := context.Background()

	// Insert 3 dispatch rows that are all-zero (L3 agent sentinel pattern).
	for i := 1; i <= 3; i++ {
		_, err := store.Insert(ctx, state.Dispatch{
			StoryID:    "z.1",
			Stage:      state.StageImplement,
			AgentRole:  "l3-agent",
			AttemptNo:  i,
			Status:     state.DispatchOK,
			Tokens:     state.TokenCounts{Input: 0, Output: 0, CacheRead: 0, CacheCreate: 0},
			DurationMS: 5_000,
		})
		if err != nil {
			t.Fatalf("Insert %d: %v", i, err)
		}
	}

	stats, err := store.CacheHitRate(ctx, "z.1")
	if err != nil {
		t.Fatalf("CacheHitRate: %v", err)
	}
	if !stats.Unknown {
		t.Errorf("Unknown = false, want true (all-zero breakdown rows must be treated as unknown)")
	}
	if stats.RowsCounted != 0 {
		t.Errorf("RowsCounted = %d, want 0", stats.RowsCounted)
	}
	if stats.RowsTotal != 3 {
		t.Errorf("RowsTotal = %d, want 3", stats.RowsTotal)
	}
	if stats.RatePercent != 0 {
		t.Errorf("RatePercent = %v, want 0 (undefined when Unknown=true)", stats.RatePercent)
	}
	if stats.StoryID != "z.1" {
		t.Errorf("StoryID = %q, want z.1", stats.StoryID)
	}
}

// TestCacheHitRate_RealNumbers asserts that rows with actual token data are
// counted correctly, and that interspersed all-zero rows are excluded from
// the rate calculation but still included in RowsTotal.
func TestCacheHitRate_RealNumbers(t *testing.T) {
	t.Parallel()
	db := newTempDB(t)
	seedStory(t, db, "z.2")
	store := sqlite.NewDispatchesStore(db)
	ctx := context.Background()

	// Row 1: all-zero (L3 sentinel — should be excluded from rate).
	_, err := store.Insert(ctx, state.Dispatch{
		StoryID:   "z.2",
		Stage:     state.StageImplement,
		AgentRole: "l3-agent",
		AttemptNo: 1,
		Status:    state.DispatchOK,
		Tokens:    state.TokenCounts{Input: 0, Output: 0, CacheRead: 0, CacheCreate: 0},
		DurationMS: 1_000,
	})
	if err != nil {
		t.Fatalf("Insert row1: %v", err)
	}

	// Row 2: real breakdown — input=1000, cache_read=500.
	// Rate contribution: cache_read/(input+cache_read) = 500/1500 = 33.33%
	_, err = store.Insert(ctx, state.Dispatch{
		StoryID:   "z.2",
		Stage:     state.StageCodeReview,
		AgentRole: "reviewer",
		AttemptNo: 1,
		Status:    state.DispatchOK,
		Tokens:    state.TokenCounts{Input: 1000, Output: 200, CacheRead: 500, CacheCreate: 50},
		DurationMS: 8_000,
	})
	if err != nil {
		t.Fatalf("Insert row2: %v", err)
	}

	// Row 3: real breakdown — input=2000, cache_read=1000.
	// Rate contribution: cumulative cache_read/(input+cache_read) = 1500/4500 = 33.33%
	_, err = store.Insert(ctx, state.Dispatch{
		StoryID:   "z.2",
		Stage:     state.StageImplement,
		AgentRole: "tdd-implementer",
		AttemptNo: 2,
		Status:    state.DispatchOK,
		Tokens:    state.TokenCounts{Input: 2000, Output: 400, CacheRead: 1000, CacheCreate: 100},
		DurationMS: 12_000,
	})
	if err != nil {
		t.Fatalf("Insert row3: %v", err)
	}

	stats, err := store.CacheHitRate(ctx, "z.2")
	if err != nil {
		t.Fatalf("CacheHitRate: %v", err)
	}
	if stats.Unknown {
		t.Errorf("Unknown = true, want false (2 rows have real data)")
	}
	if stats.RowsCounted != 2 {
		t.Errorf("RowsCounted = %d, want 2 (1 zero row excluded)", stats.RowsCounted)
	}
	if stats.RowsTotal != 3 {
		t.Errorf("RowsTotal = %d, want 3", stats.RowsTotal)
	}
	// cache_read_sum = 500+1000 = 1500; denom = (1000+500)+(2000+1000) = 4500
	// rate = 1500/4500 * 100 = 33.333...%
	wantRate := float64(1500) / float64(4500) * 100.0
	const epsilon = 0.001
	if stats.RatePercent < wantRate-epsilon || stats.RatePercent > wantRate+epsilon {
		t.Errorf("RatePercent = %.4f, want ~%.4f", stats.RatePercent, wantRate)
	}
	if stats.StoryID != "z.2" {
		t.Errorf("StoryID = %q, want z.2", stats.StoryID)
	}
}

// TestCacheHitRate_EmptyStory asserts that a story with no dispatch rows at
// all also returns Unknown=true (no data is no data).
func TestCacheHitRate_EmptyStory(t *testing.T) {
	t.Parallel()
	db := newTempDB(t)
	seedStory(t, db, "z.3")
	store := sqlite.NewDispatchesStore(db)
	ctx := context.Background()

	stats, err := store.CacheHitRate(ctx, "z.3")
	if err != nil {
		t.Fatalf("CacheHitRate: %v", err)
	}
	if !stats.Unknown {
		t.Errorf("Unknown = false, want true for story with no dispatch rows")
	}
	if stats.RowsTotal != 0 {
		t.Errorf("RowsTotal = %d, want 0", stats.RowsTotal)
	}
}
