package env_test

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	appenv "github.com/sosalejandro/bmad-story-runner-cli/application/env"
	"github.com/sosalejandro/bmad-story-runner-cli/domain/state"
	"github.com/sosalejandro/bmad-story-runner-cli/infrastructure/state/sqlite"
)

func newPool(t *testing.T) (*appenv.PortPool, *sqlite.DB) {
	t.Helper()
	db, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "bmad-state.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	envs := sqlite.NewEnvsStore(db)
	cfg := appenv.TestEnvConfig{
		PortRange:     appenv.PortRange{Start: 7600, End: 7619},
		PortsPerStory: 5,
	}
	return appenv.NewPortPool(cfg, envs), db
}

// seedStoryForPool inserts a stub story so env_allocations FK passes.
func seedStoryForPool(t *testing.T, db *sqlite.DB, id string) {
	t.Helper()
	s := sqlite.NewStoriesStore(db)
	_ = s.Insert(context.Background(), state.Story{
		ID: id, File: id + ".md", Title: id,
		Status: state.StatusPending, Complexity: state.ComplexityMedium,
	})
}

func TestPortPool_AllocatesFromStartOfRange(t *testing.T) {
	t.Parallel()
	pool, db := newPool(t)
	seedStoryForPool(t, db, "1.1")

	a, err := pool.Allocate(context.Background(), "1.1")
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	if a.PGPort != 7600 || a.RedisPort != 7601 {
		t.Fatalf("ports = pg:%d redis:%d, want 7600/7601", a.PGPort, a.RedisPort)
	}
	if a.OtelPort == nil || *a.OtelPort != 7602 {
		t.Fatalf("otel = %v, want 7602", a.OtelPort)
	}
}

func TestPortPool_SecondAllocationUsesNextBlock(t *testing.T) {
	t.Parallel()
	pool, db := newPool(t)
	seedStoryForPool(t, db, "1.1")
	seedStoryForPool(t, db, "1.2")
	ctx := context.Background()

	_, _ = pool.Allocate(ctx, "1.1")
	a2, err := pool.Allocate(ctx, "1.2")
	if err != nil {
		t.Fatalf("Allocate 2: %v", err)
	}
	if a2.PGPort != 7605 {
		t.Fatalf("second allocation pg = %d, want 7605 (next 5-port block)", a2.PGPort)
	}
}

func TestPortPool_ReusesReleasedBlock(t *testing.T) {
	t.Parallel()
	pool, db := newPool(t)
	seedStoryForPool(t, db, "x")
	seedStoryForPool(t, db, "y")
	envs := sqlite.NewEnvsStore(db)
	ctx := context.Background()

	_, _ = pool.Allocate(ctx, "x")
	_ = envs.Release(ctx, "x", "completed")

	a, err := pool.Allocate(ctx, "y")
	if err != nil {
		t.Fatalf("Allocate after release: %v", err)
	}
	if a.PGPort != 7600 {
		t.Fatalf("expected to reuse released first block (7600), got %d", a.PGPort)
	}
}

func TestPortPool_ExhaustsRangeCleanly(t *testing.T) {
	t.Parallel()
	pool, db := newPool(t)
	// Range 7600..7619, 5 ports per story = exactly 4 blocks.
	ctx := context.Background()
	for i, id := range []string{"a", "b", "c", "d"} {
		seedStoryForPool(t, db, id)
		if _, err := pool.Allocate(ctx, id); err != nil {
			t.Fatalf("Allocate #%d: %v", i, err)
		}
	}
	seedStoryForPool(t, db, "overflow")
	_, err := pool.Allocate(ctx, "overflow")
	if err == nil {
		t.Fatalf("expected exhaustion error, got nil")
	}
}

func TestPortPool_ConcurrentAllocateNoCollisions(t *testing.T) {
	t.Parallel()
	// Custom-sized pool: 8 stories × 5 ports/story = 40 ports, range 7600..7639.
	db, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "bmad-state.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	envs := sqlite.NewEnvsStore(db)
	pool := appenv.NewPortPool(appenv.TestEnvConfig{
		PortRange:     appenv.PortRange{Start: 7600, End: 7639},
		PortsPerStory: 5,
	}, envs)
	ctx := context.Background()

	const N = 8
	for i := 0; i < N; i++ {
		seedStoryForPool(t, db, fmt.Sprintf("conc-%d", i))
	}

	var wg sync.WaitGroup
	errCh := make(chan error, N)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			if _, err := pool.Allocate(ctx, fmt.Sprintf("conc-%d", idx)); err != nil {
				errCh <- err
			}
		}(i)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("concurrent Allocate: %v", err)
	}

	// Verify zero port collisions across all allocations.
	active, err := envs.Active(ctx)
	if err != nil {
		t.Fatalf("Active: %v", err)
	}
	if len(active) != N {
		t.Fatalf("got %d allocations, want %d", len(active), N)
	}
	seen := map[int]string{}
	for _, a := range active {
		if owner, dup := seen[a.PGPort]; dup {
			t.Errorf("pg_port %d allocated to both %q and %q", a.PGPort, owner, a.StoryID)
		}
		seen[a.PGPort] = a.StoryID
	}
}

func TestLoadTestEnvConfig_DefaultsWhenAbsent(t *testing.T) {
	t.Parallel()
	cfg, err := appenv.LoadTestEnvConfig(t.TempDir())
	if err != nil {
		t.Fatalf("LoadTestEnvConfig: %v", err)
	}
	if cfg.PortRange.Start != 7600 || cfg.PortsPerStory != 10 {
		t.Fatalf("defaults wrong: %+v", cfg)
	}
}
