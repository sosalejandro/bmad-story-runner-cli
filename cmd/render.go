package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/sosalejandro/bmad-story-runner-cli/domain/state"
	"github.com/sosalejandro/bmad-story-runner-cli/infrastructure/prompts"
	"github.com/sosalejandro/bmad-story-runner-cli/infrastructure/state/sqlite"
)

// newRenderCmd loads a template + resolves slots + emits rendered text.
//
// Slot data comes from three sources, merged in priority order:
//   1. --data-json  (highest — explicit override)
//   2. auto-resolved from --story / --env-story (queries SQLite)
//   3. config defaults (lowest — mode, max_parallel, reserve_ram_mb)
func newRenderCmd() *cobra.Command {
	var (
		dataJSON  string
		storyID   string
		envStory  string
		stageFlag string
		attempt   int
		mode      string
		outPath   string
	)
	cmd := &cobra.Command{
		Use:   "render <template-name>",
		Short: "Load a prompt template, resolve slots, emit rendered text",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			ctx := context.Background()
			r, err := prompts.NewRenderer()
			if err != nil {
				return err
			}

			data := map[string]any{}

			// Layer 4 (zero-defaults): pre-seed every optional slot the named
			// template might dereference. The renderer runs in
			// missingkey=error strict mode, which is great for catching
			// required-slot bugs but trips on `{{if .Optional}}` when the
			// map literally has no Optional key. Seeding with typed-nil /
			// zero-value lets the template's if-checks evaluate correctly.
			seedOptionalSlots(args[0], data)

			// Layer 3: config defaults.
			if needsConfigDefaults(args[0]) {
				if err := layerConfigDefaults(ctx, data, mode); err != nil {
					return err
				}
			}

			// Layer 2: auto-resolve story + env.
			if storyID != "" {
				if err := layerStory(ctx, data, storyID, stageFlag, attempt, mode); err != nil {
					return err
				}
			}
			if envStory != "" {
				if err := layerEnv(ctx, data, envStory); err != nil {
					return err
				}
			}

			// Layer 1: explicit override.
			if dataJSON != "" {
				var override map[string]any
				if err := json.Unmarshal([]byte(dataJSON), &override); err != nil {
					return fmt.Errorf("--data-json: %w", err)
				}
				for k, v := range override {
					data[k] = v
				}
			}

			out, err := r.Render(args[0], data)
			if err != nil {
				return err
			}

			if outPath != "" {
				if err := os.WriteFile(outPath, []byte(out), 0o644); err != nil {
					return fmt.Errorf("write --out %s: %w", outPath, err)
				}
				return nil
			}
			fmt.Print(out)
			return nil
		},
	}
	cmd.Flags().StringVar(&dataJSON, "data-json", "", "JSON object: slot overrides (highest priority)")
	cmd.Flags().StringVar(&storyID, "story", "", "auto-populate StoryID + HydratedFile from sqlite")
	cmd.Flags().StringVar(&envStory, "env-story", "", "auto-populate EnvConfig from env_allocations for this story")
	cmd.Flags().StringVar(&stageFlag, "stage", "", "current stage (informational)")
	cmd.Flags().IntVar(&attempt, "attempt", 1, "attempt number (used by retry_context)")
	cmd.Flags().StringVar(&mode, "mode", "", "mode override (defaults to config.mode)")
	cmd.Flags().StringVar(&outPath, "out", "", "write to file instead of stdout")
	addV6PersistentFlags(cmd)
	return cmd
}

func needsConfigDefaults(template string) bool {
	switch template {
	case "orchestrator_loop":
		return true
	}
	return false
}

// seedOptionalSlots pre-populates the named template's optional slots with
// typed nil / zero values so `{{if .Optional}}` evaluates cleanly under
// missingkey=error. The renderer still enforces presence of REQUIRED slots —
// that surface is preserved; this only addresses the "tested-but-not-set"
// optional pattern.
func seedOptionalSlots(template string, data map[string]any) {
	switch template {
	case "stage_implement":
		if _, ok := data["PriorAttempt"]; !ok {
			data["PriorAttempt"] = (*struct{})(nil)
		}
		if _, ok := data["EpicContext"]; !ok {
			data["EpicContext"] = ""
		}
	}
}

func layerConfigDefaults(ctx context.Context, data map[string]any, modeFlag string) error {
	db, err := openV6DB(ctx)
	if err != nil {
		return err
	}
	defer db.Close()
	cfg := sqlite.NewConfigStore(db)

	mode := modeFlag
	if mode == "" {
		if v, err := cfg.Get(ctx, "mode"); err == nil {
			mode = v
		} else {
			mode = "pragmatic"
		}
	}
	data["Mode"] = mode

	maxP, _ := cfg.Get(ctx, "max_parallel")
	if n, _ := strconv.Atoi(maxP); n > 0 {
		data["MaxParallel"] = n
	} else {
		data["MaxParallel"] = 4
	}
	reserve, _ := cfg.Get(ctx, "reserve_ram_mb")
	if n, _ := strconv.Atoi(reserve); n > 0 {
		data["ReserveRamMB"] = n
	} else {
		data["ReserveRamMB"] = 8000
	}
	return nil
}

func layerStory(ctx context.Context, data map[string]any, id, stage string, attempt int, mode string) error {
	db, err := openV6DB(ctx)
	if err != nil {
		return err
	}
	defer db.Close()
	stories := sqlite.NewStoriesStore(db)
	st, err := stories.Get(ctx, id)
	if err != nil {
		return fmt.Errorf("render --story %q: %w", id, err)
	}
	data["StoryID"] = st.ID
	if st.HydratedFile != nil {
		data["HydratedFile"] = *st.HydratedFile
	}
	if stage != "" {
		data["Stage"] = stage
	}
	data["AttemptNo"] = attempt
	if mode != "" {
		data["Mode"] = mode
	}
	return nil
}

func layerEnv(ctx context.Context, data map[string]any, storyID string) error {
	db, err := openV6DB(ctx)
	if err != nil {
		return err
	}
	defer db.Close()
	envs := sqlite.NewEnvsStore(db)
	a, err := envs.Get(ctx, storyID)
	if err != nil {
		return fmt.Errorf("render --env-story %q: %w", storyID, err)
	}
	envCfg := map[string]any{
		"PgPort":    a.PGPort,
		"RedisPort": a.RedisPort,
		"DbName":    a.DBName,
	}
	if a.OtelPort != nil {
		envCfg["OtelPort"] = *a.OtelPort
	}
	data["EnvConfig"] = envCfg
	return nil
}

// quiet linter shut-up — used by future expansion.
var _ = state.StatusComplete
