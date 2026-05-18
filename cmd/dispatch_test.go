package cmd

import (
	"math"
	"testing"

	"github.com/sosalejandro/bmad-story-runner-cli/domain/state"
)

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
