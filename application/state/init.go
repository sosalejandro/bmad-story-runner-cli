// Package state holds the v6 use cases that compose domain/state ports.
// Pure orchestration logic — no IO outside the injected adapters.
package state

import (
	"context"
	"fmt"

	"github.com/sosalejandro/bmad-story-runner-cli/domain/state"
)

// DefaultConfig is the runtime knobs seed written on `bmad init`. Values match
// the §7 reference table. Users override with `bmad config <key> <value>`.
var DefaultConfig = map[string]string{
	"mode":                          "pragmatic",
	"max_parallel":                  "4",
	"reserve_ram_mb":                "8000",
	"pr_strategy":                   "per-story",
	"batch_size":                    "3",
	"max_tdd_cycles":                "3",
	"max_qa_cycles":                 "3",
	"max_ci_retries":                "2",
	"max_review_iterations":         "3",
	"checkpoint.count_threshold":    "4",
	"env.stale_threshold_minutes":   "120",
}

// InitUseCase seeds an empty v6 state store with default config rows.
// Idempotent: re-running on an initialized DB does NOT overwrite existing
// config values; missing-only seeding lets users tune before re-init.
type InitUseCase struct {
	cfg state.Config
}

func NewInitUseCase(cfg state.Config) *InitUseCase {
	return &InitUseCase{cfg: cfg}
}

// InitResult summarizes what Init did.
type InitResult struct {
	SeededKeys  []string
	SkippedKeys []string
	DocsFolder  string
}

// Execute seeds defaults + sets docs_folder. Existing keys are NOT overwritten.
func (u *InitUseCase) Execute(ctx context.Context, docsFolder string) (*InitResult, error) {
	res := &InitResult{DocsFolder: docsFolder}

	for key, value := range DefaultConfig {
		if _, err := u.cfg.Get(ctx, key); err == nil {
			res.SkippedKeys = append(res.SkippedKeys, key)
			continue
		} else if err != state.ErrNotFound {
			return nil, fmt.Errorf("init probe %q: %w", key, err)
		}
		if err := u.cfg.Set(ctx, key, value); err != nil {
			return nil, fmt.Errorf("init seed %q: %w", key, err)
		}
		res.SeededKeys = append(res.SeededKeys, key)
	}

	// docs_folder always wins on init (user-asserted intent).
	if err := u.cfg.Set(ctx, "docs_folder", docsFolder); err != nil {
		return nil, fmt.Errorf("init set docs_folder: %w", err)
	}
	return res, nil
}
