package env

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/sosalejandro/bmad-story-runner-cli/domain/state"
)

// Sweeper implements §12.6 activity-based stale detection. An env is "alive"
// if ANY one of these signals registered within the threshold window:
//
//   - file mtime under the worktree path (any file modified recently)
//   - dispatch row returned recently for this story
//
// Git activity probe (commits / staged changes) is intentionally omitted in
// this version — the file-mtime probe catches working-tree edits, and git
// commit timestamps are already reflected in worktree file mtimes. If false
// positives appear during dogfood, swap in `git -C ... log --since`.
type Sweeper struct {
	Envs      state.Envs
	Worktrees state.Worktrees
	Dispatch  state.Dispatches
	Config    state.Config
	Now       func() time.Time // injected for deterministic tests
}

// NewSweeper wires the four ports required for activity-based detection.
func NewSweeper(envs state.Envs, worktrees state.Worktrees, d state.Dispatches, cfg state.Config) *Sweeper {
	return &Sweeper{
		Envs: envs, Worktrees: worktrees, Dispatch: d, Config: cfg,
		Now: time.Now,
	}
}

// SweepResult summarises one sweeper invocation.
type SweepResult struct {
	Swept []string `json:"swept"`
	Kept  []string `json:"kept"`
}

// Sweep walks every active env, runs the activity probe, and releases stale
// allocations (reclaim_reason = "stale"). Returns IDs of swept + kept stories.
//
// The caller is responsible for docker-compose down — sweeper only mutates
// state-store rows and emits the list. (§12.4 SRP: sweeper knows port-pool +
// dispatch + worktree state, NOT Docker.)
func (s *Sweeper) Sweep(ctx context.Context, thresholdMinutesOverride int) (*SweepResult, error) {
	threshold := thresholdMinutesOverride
	if threshold <= 0 {
		threshold = s.thresholdFromConfig(ctx)
	}
	cutoff := s.Now().Add(-time.Duration(threshold) * time.Minute)

	active, err := s.Envs.Active(ctx)
	if err != nil {
		return nil, fmt.Errorf("sweeper: load active envs: %w", err)
	}

	res := &SweepResult{}
	for _, env := range active {
		alive, err := s.aliveProbe(ctx, env.StoryID, cutoff)
		if err != nil {
			return nil, err
		}
		if alive {
			res.Kept = append(res.Kept, env.StoryID)
			continue
		}
		if err := s.Envs.Release(ctx, env.StoryID, "stale"); err != nil {
			return nil, fmt.Errorf("sweeper: release %q: %w", env.StoryID, err)
		}
		res.Swept = append(res.Swept, env.StoryID)
	}
	return res, nil
}

// aliveProbe returns true if ANY signal indicates recent activity.
func (s *Sweeper) aliveProbe(ctx context.Context, storyID string, cutoff time.Time) (bool, error) {
	// Probe 1: last dispatch return for this story.
	since, err := s.Dispatch.TokenRollupSince(ctx, cutoff) // cheap rollup; if any dispatch landed, totals will be non-zero
	if err != nil {
		return false, fmt.Errorf("sweeper: dispatch probe: %w", err)
	}
	_ = since // not enough — TokenRollupSince is sprint-wide. Use ListForStory.
	dispatches, err := s.Dispatch.ListForStory(ctx, storyID)
	if err != nil {
		return false, fmt.Errorf("sweeper: list dispatches %q: %w", storyID, err)
	}
	for _, d := range dispatches {
		if d.ReturnedAt != nil && d.ReturnedAt.After(cutoff) {
			return true, nil
		}
	}

	// Probe 2: file mtime under worktree.
	wt, err := s.Worktrees.Get(ctx, storyID)
	if err == nil { // skip probe when no worktree row exists
		recent, err := worktreeHasRecentEdit(wt.Path, cutoff)
		if err != nil {
			return false, fmt.Errorf("sweeper: fs probe %q: %w", storyID, err)
		}
		if recent {
			return true, nil
		}
	}

	return false, nil
}

func (s *Sweeper) thresholdFromConfig(ctx context.Context) int {
	v, err := s.Config.Get(ctx, "env.stale_threshold_minutes")
	if err != nil {
		return 120
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return 120
	}
	return n
}

// worktreeHasRecentEdit walks the worktree and returns true as soon as it
// finds any file whose mtime is after cutoff. Hidden directories (`.git`,
// `.worktrees`, etc.) are skipped to avoid spurious git-housekeeping hits.
func worktreeHasRecentEdit(path string, cutoff time.Time) (bool, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		// Worktree directory gone — no activity possible.
		return false, nil
	}
	var found bool
	err := filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && p != path && d.Name()[0] == '.' {
			return filepath.SkipDir
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil // be permissive on transient stat errors
		}
		if info.ModTime().After(cutoff) {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	return found, err
}
