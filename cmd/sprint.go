package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	appsprint "github.com/sosalejandro/bmad-story-runner-cli/application/sprint"
	"github.com/sosalejandro/bmad-story-runner-cli/application/sprint/inferdeps"
	"github.com/sosalejandro/bmad-story-runner-cli/domain/state"
	"github.com/sosalejandro/bmad-story-runner-cli/infrastructure/state/sqlite"
)

// newSprintCmd is the parent of the `bmad sprint <verb>` namespace.
func newSprintCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sprint <verb>",
		Short: "Sprint-level orchestration (plan from epics, run, pause/resume, status)",
	}
	cmd.AddCommand(
		newSprintPlanCmd(),
		newSprintRunCmd(),
		newSprintPauseCmd(),
		newSprintResumeCmd(),
		newSprintStatusCmd(),
		newSprintCacheBundleCmd(),
		newSprintInferDepsCmd(),
		newSprintValidateDepsCmd(),
		newSprintGraphCmd(),
	)
	addV6PersistentFlags(cmd)
	return cmd
}

// ---------- plan ----------

func newSprintPlanCmd() *cobra.Command {
	var (
		epicsPath        string
		maxP             int
		scope            string
		allowDepsMissing bool
	)
	cmd := &cobra.Command{
		Use:   "plan",
		Short: "Parse epics.md → ingest stories + dependencies + build batches",
		Long: `Reads epics.md, ingests stories + dependencies + affects rows, and
builds a deterministic batch plan respecting topo order, file-overlap
disjointness, and android-serialization.

Safety rails (issue #14):
  --scope <epic-id>          Restrict batching to one epic (e.g. --scope 1
                              picks 1.1, 1.2, …; 10.* is excluded). Useful
                              when only one epic has full frontmatter and
                              the rest still needs backfilling.

  --allow-deps-missing       Bypass the partial-coverage warning. By default,
                              ` + "`bmad sprint plan`" + ` refuses to proceed if any
                              parsed story lacks YAML frontmatter — without
                              ` + "`depends_on:`" + ` such stories collapse to topo-level-0
                              and would be dispatched alongside true Epic-1
                              top-level work (cross-epic batching bug).`,
		RunE: func(c *cobra.Command, args []string) error {
			ctx := context.Background()
			db, err := openV6DB(ctx)
			if err != nil {
				return err
			}
			defer db.Close()

			cfg := sqlite.NewConfigStore(db)
			if epicsPath == "" {
				docs, err := cfg.Get(ctx, "docs_folder")
				if err != nil {
					return fmt.Errorf("sprint plan: --epics required (docs_folder not set: %w)", err)
				}
				epicsPath = filepath.Join(docs, "epics.md")
			}
			parsedFull, err := appsprint.ParseEpicsFileFull(epicsPath)
			if err != nil {
				return err
			}
			parsed := parsedFull.Stories
			epics := parsedFull.Epics

			// Apply --scope filter FIRST so coverage analysis only flags
			// in-scope stories. A scope restricted to a fully-annotated
			// epic should not warn on un-annotated epics outside scope.
			totalBeforeScope := len(parsed)
			if scope != "" {
				parsed = appsprint.FilterByScope(parsed, scope)
				if len(parsed) == 0 {
					return fmt.Errorf("sprint plan: --scope %q matched zero stories in %s", scope, epicsPath)
				}
				// Same idea for the epic-level headers: when planning a single
				// epic, the cross-epic resolver should only see THAT epic's
				// requires_epics frontmatter (otherwise out-of-scope synthesis
				// would fan out into rows we're not touching). Restrict to the
				// requested scope.
				filteredEpics := make([]appsprint.ParsedEpic, 0, len(epics))
				for _, e := range epics {
					if e.EpicID == scope {
						filteredEpics = append(filteredEpics, e)
					}
				}
				epics = filteredEpics
			}

			// Partial-coverage safety check (issue #14, fix #1).
			coverage := appsprint.AnalyseCoverage(parsed)
			if coverage.WithoutFrontmatter > 0 && !allowDepsMissing {
				preview := coverage.UnannotatedStoryIDs
				if len(preview) > 8 {
					preview = preview[:8]
				}
				fmt.Fprintf(os.Stderr,
					"WARN: %d of %d stories have no frontmatter — they'll be treated as topo-level-0.\n"+
						"      Affected (first %d): %s%s\n"+
						"      Use --allow-deps-missing to proceed anyway, OR backfill frontmatter first.\n"+
						"      Tip: --scope <epic-id> restricts to one annotated epic.\n",
					coverage.WithoutFrontmatter, coverage.TotalStories,
					len(preview), strings.Join(preview, ", "),
					moreSuffix(coverage.UnannotatedStoryIDs, preview),
				)
				return fmt.Errorf("sprint plan: refusing to plan with %d un-annotated stories (use --allow-deps-missing to override)", coverage.WithoutFrontmatter)
			}

			if maxP <= 0 {
				if v, err := cfg.Get(ctx, "max_parallel"); err == nil {
					if n, _ := strconv.Atoi(v); n > 0 {
						maxP = n
					}
				}
				if maxP <= 0 {
					maxP = 4
				}
			}

			planner := &appsprint.Planner{
				Stories:      sqlite.NewStoriesStore(db),
				Dependencies: sqlite.NewStoryDependenciesStore(db),
				Affects:      sqlite.NewStoryAffectsStore(db),
				Batches:      sqlite.NewBatchesStore(db),
			}
			res, err := planner.PlanWithEpics(ctx, epics, parsed, maxP)
			if err != nil {
				return err
			}
			if scope != "" {
				res.Scope = scope
				res.StoriesSkippedByScope = totalBeforeScope - len(parsed)
			}
			// Surface planner-level warnings (issue #46: placeholder
			// linear-chain smell, missing referenced epics) to stderr
			// regardless of --json — the operator wants to see them
			// before deciding to dispatch.
			for _, w := range res.Warnings {
				fmt.Fprintln(os.Stderr, "warn:", w)
			}
			if jsonOutput {
				args := map[string]any{}
				if scope != "" {
					args["scope"] = scope
				}
				if maxP > 0 {
					args["max_parallel"] = maxP
				}
				return emitJSONStdout(commandPathSansRoot(c), args, res, nil)
			}
			return json.NewEncoder(os.Stdout).Encode(res)
		},
	}
	cmd.Flags().StringVar(&epicsPath, "epics", "", "epics.md path (default: <docs_folder>/epics.md)")
	cmd.Flags().IntVar(&maxP, "max-parallel", 0, "override max parallel slots per batch")
	cmd.Flags().StringVar(&scope, "scope", "", "restrict planning to one epic id (e.g. 1 → only 1.* stories)")
	cmd.Flags().BoolVar(&allowDepsMissing, "allow-deps-missing", false, "bypass the partial-frontmatter coverage check (issue #14)")
	return cmd
}

// moreSuffix renders " (+N more)" when the preview slice is shorter than the
// full list; empty string otherwise.
func moreSuffix(full, shown []string) string {
	if len(full) <= len(shown) {
		return ""
	}
	return fmt.Sprintf(" (+%d more)", len(full)-len(shown))
}

// ---------- run ----------

const sprintPausedKey = "sprint.paused"

func newSprintRunCmd() *cobra.Command {
	var mode string
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Emit the orchestrator entry prompt; pair with `bmad render orchestrator_loop`",
		Long: ``+"`bmad sprint run`"+` does NOT spawn a Claude Code session itself —
that requires Claude Code's own runtime. Instead, this prints the
suggested invocation: render the orchestrator_loop template and
pass the result to a Claude Code Agent or interactive session.`,
		RunE: func(c *cobra.Command, args []string) error {
			ctx := context.Background()
			db, err := openV6DB(ctx)
			if err != nil {
				return err
			}
			defer db.Close()

			cfg := sqlite.NewConfigStore(db)
			if mode != "" {
				if err := cfg.Set(ctx, "mode", mode); err != nil {
					return err
				}
			}
			_ = cfg.Delete(ctx, sprintPausedKey)

			activeMode, _ := cfg.Get(ctx, "mode")
			return json.NewEncoder(os.Stdout).Encode(map[string]any{
				"action": "render_and_dispatch",
				"mode":   activeMode,
				"steps": []string{
					"bmad render orchestrator_loop > /tmp/orchestrator-prompt.md",
					"# Launch a Claude Code session with the rendered prompt as system message",
					"# (interactive or via Agent tool — Claude Code's choice)",
				},
				"sprint_paused": false,
			})
		},
	}
	cmd.Flags().StringVar(&mode, "mode", "", "set persistent mode (pragmatic|strict) before starting")
	return cmd
}

// ---------- pause ----------

func newSprintPauseCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "pause",
		Short: "Set sprint.paused = true (orchestrator loop checks this between iterations)",
		RunE: func(c *cobra.Command, args []string) error {
			ctx := context.Background()
			db, err := openV6DB(ctx)
			if err != nil {
				return err
			}
			defer db.Close()
			cfg := sqlite.NewConfigStore(db)
			if err := cfg.Set(ctx, sprintPausedKey, "true"); err != nil {
				return err
			}
			if jsonOutput {
				return emitJSONStdout(commandPathSansRoot(c), nil,
					map[string]any{"ok": true, "paused": true}, nil)
			}
			fmt.Println("sprint paused (next iteration will exit gracefully)")
			return nil
		},
	}
}

// ---------- resume ----------

// newSprintResumeCmd unpauses the sprint AND clears any open §12.5
// checkpoint rows (issue #71).
//
// Why both in one command: prior to this fix, `bmad sprint resume` only
// cleared `sprint.paused` but left `user_decision IS NULL` rows alone, so
// the very next `bmad sprint status --json` re-surfaced
// `unresolved_checkpoint` and the orchestrator loop would call `resume`
// again on each iteration without making forward progress.
//
// Decision stamped: `state.DecisionContinue` — the operator running
// `sprint resume` is implicitly declaring "I want to proceed past this
// halt". Operators who instead want `adjust` or `halt` should use
// `bmad checkpoint decide` (or its successor) directly — this command is
// the happy-path shortcut for the most common case.
//
// All-or-most-recent rule: ResolveAllUnresolved stamps every NULL row in a
// single UPDATE. We choose ALL (not just MAX(id)) because a sprint with
// multiple stuck checkpoints would otherwise need N resume calls to clear;
// the orchestrator can re-Fire if it genuinely needs a new halt.
func newSprintResumeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "resume",
		Short: "Clear sprint.paused + resolve any open §12.5 checkpoint rows (issue #71)",
		RunE: func(c *cobra.Command, args []string) error {
			ctx := context.Background()
			db, err := openV6DB(ctx)
			if err != nil {
				return err
			}
			defer db.Close()
			cfg := sqlite.NewConfigStore(db)
			if err := cfg.Delete(ctx, sprintPausedKey); err != nil {
				return err
			}
			checkpoints := sqlite.NewCheckpointsStore(db)
			cleared, err := checkpoints.ResolveAllUnresolved(ctx,
				state.DecisionContinue, time.Now().UTC())
			if err != nil {
				return fmt.Errorf("sprint resume: resolve checkpoints: %w", err)
			}
			if jsonOutput {
				return emitJSONStdout(commandPathSansRoot(c), nil,
					map[string]any{
						"ok":                    true,
						"paused":                false,
						"checkpoints_resolved":  cleared,
						"checkpoint_decision":   string(state.DecisionContinue),
					}, nil)
			}
			if cleared > 0 {
				fmt.Printf("sprint resumed (cleared %d open checkpoint(s) → %s)\n",
					cleared, state.DecisionContinue)
			} else {
				fmt.Println("sprint resumed")
			}
			return nil
		},
	}
}

// ---------- status ----------

func newSprintStatusCmd() *cobra.Command {
	var raw bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Sprint-aggregate view (batches, in-flight, blocked, tokens, checkpoints)",
		RunE: func(c *cobra.Command, args []string) error {
			ctx := context.Background()
			db, err := openV6DB(ctx)
			if err != nil {
				return err
			}
			defer db.Close()

			stories := sqlite.NewStoriesStore(db)
			batches := sqlite.NewBatchesStore(db)
			dispatches := sqlite.NewDispatchesStore(db)
			checkpoints := sqlite.NewCheckpointsStore(db)
			cfg := sqlite.NewConfigStore(db)

			all, err := stories.List(ctx, state.StoryFilter{})
			if err != nil {
				return err
			}
			counts := map[string]int{}
			for _, st := range all {
				counts[string(st.Status)]++
			}

			batchList, _ := batches.List(ctx)
			batchSummary := map[string]int{}
			for _, b := range batchList {
				batchSummary[string(b.Status)]++
			}

			tokens, _ := dispatches.TokenRollupSince(ctx, time.Unix(0, 0))
			// Issue #42 (Path 3): compute cache hit rate using sentinel detection
			// so all-zero TOKEN_BREAKDOWN rows from L3 agents are treated as
			// "value unknown" rather than being aggregated into a false "0%".
			cacheStats, _ := dispatches.CacheHitRateSince(ctx, time.Unix(0, 0))

			unresolved, _ := checkpoints.Unresolved(ctx)
			paused, _ := cfg.Get(ctx, sprintPausedKey)
			mode, _ := cfg.Get(ctx, "mode")

			// Issue #15: surface the 4-axis breakdown + cache_hit_rate so
			// downstream dashboards can see cache health at a glance.
			// Issue #42: replace the naive cacheHitRate(tokens) call with the
			// sentinel-aware aggregate; "unknown" when no non-zero rows exist.
			var cacheHitRateVal any
			if cacheStats.Unknown {
				cacheHitRateVal = "unknown (issue #42)"
			} else {
				cacheHitRateVal = cacheStats.RatePercent
			}
			tokenBreakdown := map[string]any{
				"input":          tokens.Input,
				"output":         tokens.Output,
				"cache_read":     tokens.CacheRead,
				"cache_create":   tokens.CacheCreate,
				"total":          tokens.Input + tokens.Output + tokens.CacheRead + tokens.CacheCreate,
				"cache_hit_rate": cacheHitRateVal,
				"cache_hit_rate_rows_counted": cacheStats.RowsCounted,
				"cache_hit_rate_rows_total":   cacheStats.RowsTotal,
			}

			report := map[string]any{
				"mode":                  mode,
				"paused":                paused == "true",
				"total_stories":         len(all),
				"by_status":             counts,
				"batches":               batchSummary,
				"unresolved_checkpoint": unresolved,
				"tokens":                tokenBreakdown,
			}
			if jsonOutput {
				return emitJSONStdout(commandPathSansRoot(c), nil, report, nil)
			}
			if raw {
				return json.NewEncoder(os.Stdout).Encode(report)
			}
			return printSprintTable(report, counts, batchSummary, tokens, cacheStats)
		},
	}
	cmd.Flags().BoolVar(&raw, "raw", false, "emit raw JSON")
	return cmd
}

func printSprintTable(report map[string]any, counts, batches map[string]int, tokens state.TokenCounts, cacheStats sqlite.CacheHitRateStats) error {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(w, "mode\t%v\n", report["mode"])
	fmt.Fprintf(w, "paused\t%v\n", report["paused"])
	fmt.Fprintf(w, "total stories\t%d\n", report["total_stories"])
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "BY STATUS")
	for status, n := range counts {
		fmt.Fprintf(w, "  %s\t%d\n", status, n)
	}
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "BATCHES")
	for status, n := range batches {
		fmt.Fprintf(w, "  %s\t%d\n", status, n)
	}
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "TOKENS (cumulative)")
	fmt.Fprintf(w, "  input\t%d\n", tokens.Input)
	fmt.Fprintf(w, "  output\t%d\n", tokens.Output)
	fmt.Fprintf(w, "  cache read\t%d\n", tokens.CacheRead)
	fmt.Fprintf(w, "  cache create\t%d\n", tokens.CacheCreate)
	fmt.Fprintf(w, "  total\t%d\n", tokens.Input+tokens.Output+tokens.CacheRead+tokens.CacheCreate)
	// Issue #42 (Path 3): render "unknown (issue #42)" instead of "0%" when all
	// TOKEN_BREAKDOWN rows are all-zero (L3 agents can't introspect their usage).
	if cacheStats.Unknown {
		fmt.Fprintf(w, "  cache hit rate\tunknown (issue #42) — %d/%d rows have breakdown data\n",
			cacheStats.RowsCounted, cacheStats.RowsTotal)
	} else {
		fmt.Fprintf(w, "  cache hit rate\t%.1f%% (%d/%d rows)\n",
			cacheStats.RatePercent, cacheStats.RowsCounted, cacheStats.RowsTotal)
	}
	if cp, ok := report["unresolved_checkpoint"].(*state.Checkpoint); ok && cp != nil {
		fmt.Fprintln(w, "")
		fmt.Fprintln(w, "UNRESOLVED CHECKPOINT — run `bmad sprint resume` after deciding")
	}
	return w.Flush()
}

// ensure ignored import surfaces a build error if unused upstream
var _ = strings.HasPrefix
var _ = errors.Is

// ---------- cache-bundle ----------

func newSprintCacheBundleCmd() *cobra.Command {
	var (
		outPath  string
		docsDir  string
	)
	cmd := &cobra.Command{
		Use:   "cache-bundle",
		Short: "Concatenate standing corpus into one file the hydrator can read once per dispatch",
		Long: `Builds a deterministic concatenation of the v6-cutover canon docs
(architecture, audit, supplement, decision tree, saga mapping) into a
single file. The stage_hydrate template injects the bundle path so the
L3 agent does one Read instead of 5+ — and the byte-identical prefix
across sprints gives Claude Code's auto-prompt-caching a hit point on
subsequent hydrator dispatches in the same session.

Stores the bundle path in config.sprint.cache_bundle_path so future
renders can find it automatically. Default output:
  <docs_folder>/sprint-cache-bundle.md`,
		RunE: func(c *cobra.Command, args []string) error {
			ctx := context.Background()
			db, err := openV6DB(ctx)
			if err != nil {
				return err
			}
			defer db.Close()
			cfg := sqlite.NewConfigStore(db)

			if docsDir == "" {
				v, err := cfg.Get(ctx, "docs_folder")
				if err != nil {
					return fmt.Errorf("cache-bundle: docs_folder not set; provide --docs or run `bmad init`")
				}
				docsDir = v
			}
			if outPath == "" {
				outPath = filepath.Join(docsDir, "sprint-cache-bundle.md")
			}

			res, err := appsprint.BuildCacheBundle(docsDir, outPath, nil)
			if err != nil {
				return err
			}
			if err := cfg.Set(ctx, "sprint.cache_bundle_path", outPath); err != nil {
				return fmt.Errorf("cache-bundle: persist config: %w", err)
			}
			return json.NewEncoder(os.Stdout).Encode(res)
		},
	}
	cmd.Flags().StringVar(&outPath, "out", "", "output path (default: <docs_folder>/sprint-cache-bundle.md)")
	cmd.Flags().StringVar(&docsDir, "docs", "", "source docs folder (default: config.docs_folder)")
	return cmd
}

// ---------- infer-deps ----------

// newSprintInferDepsCmd wires `bmad sprint infer-deps` — parses an
// epics.md file for **Given** / **Refs** / epic-level prose cues and
// emits suggested `depends_on:` YAML patches. See issue #19.
func newSprintInferDepsCmd() *cobra.Command {
	var (
		epicsPath     string
		scope         string
		apply         bool
		backup        bool
		intraEpic     bool
	)
	cmd := &cobra.Command{
		Use:   "infer-deps",
		Short: "Suggest depends_on patches by parsing epics.md prose cues (closes #19)",
		Long: `Walks epics.md and per-story extracts dependency cues from the
**Given** / **Refs** prose plus the parent epic header, then emits
suggested ` + "`depends_on:`" + ` YAML patches.

Dry-run (default): suggestions print to stdout — paste into epics.md.
With ` + "`--apply`" + ` the existing depends_on line is rewritten in place;
pair with ` + "`--backup`" + ` to keep a ` + "`.bak`" + ` companion.

Recogniser confidence:
  HIGH   explicit "Story X.Y" mention inside a **Given** / **Refs** line
  MEDIUM "Slice <s>" or "Epic N" mention resolved to last story of that epic
  LOW    intra-epic fallback (Story X.Y → X.(Y-1)) — only when --intra-epic`,
		RunE: func(c *cobra.Command, args []string) error {
			ctx := context.Background()
			// Resolve epicsPath from config.docs_folder when the flag is
			// omitted, mirroring `bmad sprint plan`'s convention.
			if epicsPath == "" {
				db, err := openV6DB(ctx)
				if err != nil {
					return err
				}
				defer db.Close()
				cfg := sqlite.NewConfigStore(db)
				docs, err := cfg.Get(ctx, "docs_folder")
				if err != nil {
					return fmt.Errorf("sprint infer-deps: --epics required (docs_folder not set: %w)", err)
				}
				epicsPath = filepath.Join(docs, "epics.md")
			}

			res, warnings, err := inferdeps.Run(inferdeps.RunOptions{
				EpicsPath:         epicsPath,
				ScopeEpic:         scope,
				Apply:             apply,
				Backup:            backup,
				IntraEpicFallback: intraEpic,
			})
			if err != nil {
				return err
			}

			if jsonOutput {
				args := map[string]any{
					"epics":       epicsPath,
					"apply":       apply,
					"backup":      backup,
					"intra_epic":  intraEpic,
				}
				if scope != "" {
					args["scope"] = scope
				}
				return emitJSONStdout(commandPathSansRoot(c), args, res, warnings)
			}

			for _, w := range warnings {
				fmt.Fprintln(os.Stderr, "warn:", w)
			}
			return inferdeps.EmitSummary(os.Stdout, res)
		},
	}
	cmd.Flags().StringVar(&epicsPath, "epics", "", "epics.md path (default: <docs_folder>/epics.md)")
	cmd.Flags().StringVar(&scope, "scope", "", "restrict to one epic id (e.g. 4 → only 4.* stories)")
	cmd.Flags().BoolVar(&apply, "apply", false, "rewrite depends_on in place (default: dry-run)")
	cmd.Flags().BoolVar(&backup, "backup", false, "write a .bak companion when --apply (no-op otherwise)")
	cmd.Flags().BoolVar(&intraEpic, "intra-epic", false, "enable LOW-confidence Story X.Y → X.(Y-1) fallback")
	return cmd
}
