package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

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
		dataJSON          string
		storyID           string
		envStory          string
		stageFlag         string
		attempt           int
		mode              string
		outPath           string
		debugCachePrefix  bool
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
			if k, _ := c.Flags().GetString("idempotency-key"); k != "" {
				data["IdempotencyKey"] = k
			}

			// Optional-slot zero-defaults are seeded inside Render() itself
			// (single source of truth at the library layer).

			// Layer 3: config defaults.
			if needsConfigDefaults(args[0]) {
				if err := layerConfigDefaults(ctx, data, mode); err != nil {
					return err
				}
			}

			// Layer 3.5: template-specific config auto-pull.
			//   stage_hydrate → CacheBundlePath from config.sprint.cache_bundle_path
			if args[0] == "stage_hydrate" {
				if err := layerCacheBundle(ctx, data); err != nil {
					return err
				}
			}
			// Layer 3.6: per-story context bundle auto-pull (if --story is
			// set and a bundle file exists at the conventional path).
			if storyID != "" && (args[0] == "stage_hydrate" || args[0] == "stage_implement") {
				layerStoryContextBundle(data, storyID)
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

			if debugCachePrefix {
				out = annotateCacheBoundary(out)
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
	cmd.Flags().BoolVar(&debugCachePrefix, "debug-cache-prefix", false, "annotate the cache-boundary in the rendered output (issue #20). The stable PREFIX (cache-eligible — byte-identical across dispatches of this stage) is wrapped with `=== STABLE PREFIX ===` markers; the variable SUFFIX (per-story slot data — cache-busts each dispatch) is wrapped with `=== VARIABLE SUFFIX ===` markers. Used for diagnosing prompt-cache hit-rate issues.")
	cmd.Flags().String("idempotency-key", "", "idempotency key from `dispatch begin` — injected into the rendered prompt so the L3 agent echoes it back")
	addV6PersistentFlags(cmd)
	return cmd
}

// cacheBoundaryMarker matches the literal HEADING line every stage_*.tmpl
// uses to open its per-story suffix. The marker doubles as the cache
// boundary: every byte before the marker is stable across dispatches of the
// same stage; every byte from the marker onward is per-story.
//
// The marker is anchored with a leading newline so prose mentions of the
// section name (e.g. `read "## Story dispatch" last`) inside the stable
// prefix don't get matched.
//
// For non-stage templates (orchestrator_loop, retry_context), there is no
// boundary — the entire template is either cache-stable (orchestrator_loop
// is per-session-stable) or always nested inside another template
// (retry_context). We surface the whole text as "stable" in that case.
const cacheBoundaryMarker = "\n## Story dispatch\n"

// annotateCacheBoundary wraps the rendered output with explicit STABLE PREFIX
// / VARIABLE SUFFIX markers so operators can see exactly where the prompt
// stops being cache-eligible.
func annotateCacheBoundary(rendered string) string {
	idx := strings.Index(rendered, cacheBoundaryMarker)
	if idx < 0 {
		// No marker means the template isn't a per-story stage — treat the
		// whole thing as the stable prefix.
		return "=== STABLE PREFIX (cache-eligible — no per-story variance) ===\n" +
			rendered +
			"\n=== END (no variable suffix) ===\n"
	}
	// Include the leading newline in the prefix so the suffix starts at the
	// "## Story dispatch" heading itself.
	prefix := rendered[:idx+1]
	suffix := rendered[idx+1:]
	return "=== STABLE PREFIX (cache-eligible — byte-identical across dispatches of this stage) ===\n" +
		prefix +
		"=== VARIABLE SUFFIX (per-story — cache-busts here onward) ===\n" +
		suffix +
		"=== END ===\n"
}

func needsConfigDefaults(template string) bool {
	switch template {
	case "orchestrator_loop":
		return true
	}
	return false
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
	// ClaimerID is optional in the template — seed empty default so
	// missingkey=error doesn't trip when the caller didn't supply it.
	if _, ok := data["ClaimerID"]; !ok {
		data["ClaimerID"] = "orchestrator"
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

// layerStoryContextBundle seeds data["StoryContextBundlePath"] from the
// conventional bundle location (_bmad-output/context-bundles/<id>.md). If
// the file doesn't exist, the slot stays empty and the template falls
// back to its sources-individually instructions.
func layerStoryContextBundle(data map[string]any, storyID string) {
	// Match the cmd/story.go DefaultStoryContextBundleDir constant.
	candidate := filepath.Join("_bmad-output", "context-bundles", storyID+".md")
	if _, err := os.Stat(candidate); err != nil {
		return
	}
	abs, err := filepath.Abs(candidate)
	if err != nil {
		return
	}
	data["StoryContextBundlePath"] = abs
}

// layerCacheBundle pulls the configured sprint.cache_bundle_path into
// data["CacheBundlePath"] if present. Empty (or unset) is left empty —
// the template falls back to listing individual source files.
func layerCacheBundle(ctx context.Context, data map[string]any) error {
	db, err := openV6DB(ctx)
	if err != nil {
		return err
	}
	defer db.Close()
	cfg := sqlite.NewConfigStore(db)
	v, err := cfg.Get(ctx, "sprint.cache_bundle_path")
	if err != nil { // not configured yet → leave empty (template handles fallback)
		return nil
	}
	if v != "" {
		data["CacheBundlePath"] = v
	}
	return nil
}

// quiet linter shut-up — used by future expansion.
var _ = state.StatusComplete
