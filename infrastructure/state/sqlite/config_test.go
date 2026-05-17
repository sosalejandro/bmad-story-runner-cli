package sqlite_test

import (
	"context"
	"errors"
	"testing"

	"github.com/sosalejandro/bmad-story-runner-cli/domain/state"
	"github.com/sosalejandro/bmad-story-runner-cli/infrastructure/state/sqlite"
)

func newConfigStore(t *testing.T) *sqlite.ConfigStore {
	t.Helper()
	return sqlite.NewConfigStore(newTempDB(t))
}

func TestConfig_GetUnknownReturnsErrNotFound(t *testing.T) {
	t.Parallel()
	cfg := newConfigStore(t)

	_, err := cfg.Get(context.Background(), "missing.key")
	if !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("Get unknown key err = %v, want ErrNotFound", err)
	}
}

func TestConfig_SetThenGetRoundTrips(t *testing.T) {
	t.Parallel()
	cfg := newConfigStore(t)
	ctx := context.Background()

	if err := cfg.Set(ctx, "mode", "strict"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := cfg.Get(ctx, "mode")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != "strict" {
		t.Fatalf("Get = %q, want %q", got, "strict")
	}
}

func TestConfig_SetIsUpsert(t *testing.T) {
	t.Parallel()
	cfg := newConfigStore(t)
	ctx := context.Background()

	if err := cfg.Set(ctx, "max_parallel", "4"); err != nil {
		t.Fatalf("first Set: %v", err)
	}
	if err := cfg.Set(ctx, "max_parallel", "2"); err != nil {
		t.Fatalf("overwrite Set: %v", err)
	}

	got, err := cfg.Get(ctx, "max_parallel")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != "2" {
		t.Fatalf("Get after overwrite = %q, want %q", got, "2")
	}
}

func TestConfig_AllReturnsKeysSorted(t *testing.T) {
	t.Parallel()
	cfg := newConfigStore(t)
	ctx := context.Background()

	pairs := map[string]string{
		"mode":                          "pragmatic",
		"max_parallel":                  "4",
		"checkpoint.count_threshold":    "4",
		"env.stale_threshold_minutes":   "120",
	}
	for k, v := range pairs {
		if err := cfg.Set(ctx, k, v); err != nil {
			t.Fatalf("seed Set(%q): %v", k, err)
		}
	}

	all, err := cfg.All(ctx)
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(all) != len(pairs) {
		t.Fatalf("All len = %d, want %d", len(all), len(pairs))
	}
	for i := 1; i < len(all); i++ {
		if all[i-1].Key >= all[i].Key {
			t.Fatalf("All not sorted: %q >= %q at i=%d", all[i-1].Key, all[i].Key, i)
		}
	}
}

func TestConfig_DeleteRemovesKey(t *testing.T) {
	t.Parallel()
	cfg := newConfigStore(t)
	ctx := context.Background()

	if err := cfg.Set(ctx, "pr_strategy", "batch"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := cfg.Delete(ctx, "pr_strategy"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := cfg.Get(ctx, "pr_strategy"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("Get after Delete err = %v, want ErrNotFound", err)
	}
}

func TestConfig_DeleteUnknownIsNoOp(t *testing.T) {
	t.Parallel()
	cfg := newConfigStore(t)

	if err := cfg.Delete(context.Background(), "never.set"); err != nil {
		t.Fatalf("Delete unknown should be no-op, got: %v", err)
	}
}

// Compile-time check: ConfigStore satisfies the domain port.
var _ state.Config = (*sqlite.ConfigStore)(nil)
