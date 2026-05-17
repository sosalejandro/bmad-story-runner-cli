package env

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/sosalejandro/bmad-story-runner-cli/domain/state"
)

// PortPool allocates contiguous port blocks from a per-repo range, persisting
// the allocation through state.Envs. Pure allocator — port-pool knows nothing
// about Docker (§12.4 SRP split).
//
// Concurrency: the same-process race (two goroutines both seeing the same
// "free" block before either Reserve completes) is guarded by `mu`. The
// cross-process race (two separate `bmad env up` invocations) is caught by
// partial unique indexes on env_allocations.{pg,redis,otel}_port (migration
// 0002) that turn it into a UNIQUE violation; Allocate retries on that.
type PortPool struct {
	cfg  TestEnvConfig
	envs state.Envs
	mu   sync.Mutex
}

func NewPortPool(cfg TestEnvConfig, envs state.Envs) *PortPool {
	return &PortPool{cfg: cfg, envs: envs}
}

// maxAllocateRetries caps the retry loop for cross-process port races.
// Each retry re-reads the in-use set and picks a new block, so progress is
// guaranteed up to the point of pool exhaustion (which surfaces its own error).
const maxAllocateRetries = 8

// Allocate reserves the next free ports_per_story-sized block for storyID,
// writes the env_allocations row, and returns the assigned ports.
//
// The block layout is positional: pg = block[0], redis = block[1],
// otel = block[2] (or nil when ports_per_story < 3).
func (p *PortPool) Allocate(ctx context.Context, storyID string) (state.EnvAllocation, error) {
	if storyID == "" {
		return state.EnvAllocation{}, fmt.Errorf("port pool: storyID required")
	}
	if p.cfg.PortsPerStory < 2 {
		return state.EnvAllocation{}, fmt.Errorf("port pool: ports_per_story must be >= 2 (got %d)", p.cfg.PortsPerStory)
	}

	// Same-process lock — serialize allocation within this CLI invocation.
	// Cross-process races are caught by the partial unique indexes (mig 0002)
	// and the retry loop below.
	p.mu.Lock()
	defer p.mu.Unlock()

	var lastErr error
	for attempt := 0; attempt < maxAllocateRetries; attempt++ {
		used, err := p.envs.InUsePorts(ctx)
		if err != nil {
			return state.EnvAllocation{}, fmt.Errorf("port pool: load in-use: %w", err)
		}
		usedSet := make(map[int]bool, len(used))
		for _, port := range used {
			usedSet[port] = true
		}

		block, err := p.findFreeBlock(usedSet)
		if err != nil {
			return state.EnvAllocation{}, err
		}

		alloc := state.EnvAllocation{
			StoryID:   storyID,
			PGPort:    block[0],
			RedisPort: block[1],
			DBName:    dbName(storyID),
		}
		if len(block) >= 3 {
			otel := block[2]
			alloc.OtelPort = &otel
		}

		if err := p.envs.Reserve(ctx, alloc); err != nil {
			if isPortUniqueViolation(err) {
				// Another process won this block — re-read in-use and try
				// again with a fresh free-block computation.
				lastErr = err
				continue
			}
			return state.EnvAllocation{}, fmt.Errorf("port pool: persist allocation: %w", err)
		}
		return alloc, nil
	}
	return state.EnvAllocation{}, fmt.Errorf(
		"port pool: exhausted %d retries against cross-process contention; last error: %w",
		maxAllocateRetries, lastErr)
}

// isPortUniqueViolation tests if an error came from one of the env-port
// partial unique indexes (migration 0002). modernc.org/sqlite surfaces these
// as "UNIQUE constraint failed: env_allocations.pg_port" (etc.).
func isPortUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "UNIQUE constraint failed: env_allocations.pg_port") ||
		strings.Contains(msg, "UNIQUE constraint failed: env_allocations.redis_port") ||
		strings.Contains(msg, "UNIQUE constraint failed: env_allocations.otel_port")
}

// findFreeBlock walks the configured range in ports_per_story-sized steps,
// returning the first block whose every member is unused.
func (p *PortPool) findFreeBlock(used map[int]bool) ([]int, error) {
	for start := p.cfg.PortRange.Start; start+p.cfg.PortsPerStory-1 <= p.cfg.PortRange.End; start += p.cfg.PortsPerStory {
		free := true
		for offset := 0; offset < p.cfg.PortsPerStory; offset++ {
			if used[start+offset] {
				free = false
				break
			}
		}
		if free {
			block := make([]int, p.cfg.PortsPerStory)
			for i := range block {
				block[i] = start + i
			}
			return block, nil
		}
	}
	return nil, fmt.Errorf("port pool: no free block of size %d in range [%d, %d]",
		p.cfg.PortsPerStory, p.cfg.PortRange.Start, p.cfg.PortRange.End)
}

// dbName converts a story id like "4.1.identity" to "story_4_1_identity"
// (sqlite/postgres database names disallow dots).
func dbName(storyID string) string {
	return "story_" + strings.NewReplacer(".", "_", "-", "_", "/", "_").Replace(storyID)
}
