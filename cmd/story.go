package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"text/tabwriter"

	"github.com/spf13/cobra"

	appsprint "github.com/sosalejandro/bmad-story-runner-cli/application/sprint"
	appstate "github.com/sosalejandro/bmad-story-runner-cli/application/state"
	"github.com/sosalejandro/bmad-story-runner-cli/domain/state"
	"github.com/sosalejandro/bmad-story-runner-cli/infrastructure/state/sqlite"
)

// newStoryCmd is the parent of the `bmad story <verb>` namespace.
func newStoryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "story <verb>",
		Short: "Per-story lifecycle commands (status, hydrate, next, complete, ...)",
	}
	cmd.AddCommand(
		newStoryStatusCmd(),
		newStoryHydrateCmd(),
		newStoryNextCmd(),
		newStoryCheckpointCmd(),
		newStorySetStatusCmd(),
		newStoryCompleteCmd(),
		newStoryApplicableStagesCmd(),
		newStorySetTypeCmd(),
		newStoryContextBundleCmd(),
	)
	addV6PersistentFlags(cmd)
	return cmd
}

// ---------- shared helpers ----------

func openStoryService(ctx context.Context) (*appstate.StoryService, func(), error) {
	db, err := openV6DB(ctx)
	if err != nil {
		return nil, nil, err
	}
	svc := &appstate.StoryService{
		Stories:      sqlite.NewStoriesStore(db),
		Dependencies: sqlite.NewStoryDependenciesStore(db),
		Affects:      sqlite.NewStoryAffectsStore(db),
		Concerns:     sqlite.NewStoryConcernsStore(db),
		RetryCounts:  sqlite.NewStoryRetryCountsStore(db),
		Config:       sqlite.NewConfigStore(db),
		Checkpoints:  sqlite.NewCheckpointsStore(db),
	}
	cleanup := func() { _ = db.Close() }
	return svc, cleanup, nil
}

// ---------- status ----------

func newStoryStatusCmd() *cobra.Command {
	var (
		statusFlag string
		stageFlag  string
		hasEnv     bool
		hasEnvSet  bool
	)
	cmd := &cobra.Command{
		Use:   "status [<story-id>]",
		Short: "Show story status (table for many, detail for one)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			hasEnvSet = c.Flags().Changed("has-env")
			ctx := context.Background()
			svc, cleanup, err := openStoryService(ctx)
			if err != nil {
				return err
			}
			defer cleanup()

			if len(args) == 1 {
				return printStoryDetail(ctx, svc, args[0])
			}

			f := state.StoryFilter{}
			if statusFlag != "" {
				st := state.Status(statusFlag)
				f.Status = &st
			}
			if stageFlag != "" {
				sg := state.Stage(stageFlag)
				f.CurrentStage = &sg
			}
			if hasEnvSet {
				f.HasEnv = &hasEnv
			}
			return printStoryTable(ctx, svc, f)
		},
	}
	cmd.Flags().StringVar(&statusFlag, "status", "", "filter by status")
	cmd.Flags().StringVar(&stageFlag, "stage", "", "filter by current stage")
	cmd.Flags().BoolVar(&hasEnv, "has-env", false, "filter by env-up state")
	return cmd
}

func printStoryTable(ctx context.Context, svc *appstate.StoryService, f state.StoryFilter) error {
	rows, err := svc.Stories.List(ctx, f)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		fmt.Println("(no stories match filter)")
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tSTATUS\tSTAGE\tCOMPLEXITY\tCI\tTITLE")
	for _, st := range rows {
		stage := "-"
		if st.CurrentStage != nil {
			stage = string(*st.CurrentStage)
		}
		ci := "n"
		if st.CIPassed {
			ci = "y"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
			st.ID, st.Status, stage, st.Complexity, ci, st.Title)
	}
	return w.Flush()
}

func printStoryDetail(ctx context.Context, svc *appstate.StoryService, id string) error {
	st, err := svc.Stories.Get(ctx, id)
	if errors.Is(err, state.ErrNotFound) {
		fmt.Fprintf(os.Stderr, "story %q not found\n", id)
		os.Exit(1)
	}
	if err != nil {
		return err
	}
	deps, _ := svc.Dependencies.Of(ctx, id)
	affects, _ := svc.Affects.Of(ctx, id)
	concerns, _ := svc.Concerns.Of(ctx, id)
	retries, _ := svc.RetryCounts.Get(ctx, id)

	out := map[string]any{
		"id":               st.ID,
		"file":             st.File,
		"title":            st.Title,
		"status":           st.Status,
		"current_stage":    st.CurrentStage,
		"complexity":       st.Complexity,
		"parallel_group":   st.ParallelGroup,
		"hydrated_file":    st.HydratedFile,
		"resource_budget":  st.ResourceBudget,
		"requires_android": st.RequiresAndroid,
		"ci_passed":        st.CIPassed,
		"commit_hash":      st.CommitHash,
		"pr_url":           st.PRURL,
		"completed_at":     st.CompletedAt,
		"created_at":       st.CreatedAt,
		"updated_at":       st.UpdatedAt,
		"depends_on":       deps,
		"affects":          affects,
		"concerns":         concerns,
		"retry_counts":     retries,
	}
	return json.NewEncoder(os.Stdout).Encode(out)
}

// ---------- hydrate ----------

func newStoryHydrateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "hydrate <story-id>",
		Short: "Mark a story as hydrating + emit the dispatch instruction",
		Long: `Sets status=hydrating + current_stage=hydrate. Emits a one-line
JSON instruction the orchestrator agent uses to dispatch the
story-hydrator L3 agent. Idempotent — re-running before the L3
agent returns produces the same instruction.`,
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			ctx := context.Background()
			svc, cleanup, err := openStoryService(ctx)
			if err != nil {
				return err
			}
			defer cleanup()

			id := args[0]
			if _, err := svc.Stories.Get(ctx, id); err != nil {
				return fmt.Errorf("hydrate %q: %w", id, err)
			}
			if err := svc.Stories.SetStatus(ctx, id, state.StatusHydrating); err != nil {
				return err
			}
			stage := state.StageHydrate
			if err := svc.Stories.SetCurrentStage(ctx, id, &stage); err != nil {
				return err
			}
			return json.NewEncoder(os.Stdout).Encode(map[string]string{
				"action":   "dispatch",
				"agent":    "story-hydrator",
				"story_id": id,
				"stage":    string(stage),
			})
		},
	}
}

// ---------- next ----------

func newStoryNextCmd() *cobra.Command {
	var (
		max     int
		claim   bool
		claimer string
	)
	cmd := &cobra.Command{
		Use:   "next",
		Short: "Emit a parallel-eligible next-action batch",
		Long: `By default emits candidates WITHOUT mutating state (--claim=false).
With --claim, atomically marks the picked stories claimed_at + claimed_by
in a single transaction so two orchestrator iterations cannot both pick the
same story. Always pass --claim in production orchestrator loops — the only
no-claim use case is read-only inspection.`,
		RunE: func(c *cobra.Command, args []string) error {
			ctx := context.Background()
			svc, cleanup, err := openStoryService(ctx)
			if err != nil {
				return err
			}
			defer cleanup()

			actions, err := svc.Next(ctx, max)
			if err != nil {
				return err
			}

			if claim && len(actions) > 0 {
				eligible := make([]string, 0, len(actions))
				for _, a := range actions {
					eligible = append(eligible, a.StoryID)
				}
				claimerName := claimer
				if claimerName == "" {
					claimerName = "orchestrator"
				}
				claimed, err := svc.Stories.ClaimUnclaimedPending(ctx, eligible, len(eligible), claimerName)
				if err != nil {
					return fmt.Errorf("story next --claim: %w", err)
				}
				// Filter actions down to the actually-claimed set (in case a
				// concurrent orchestrator grabbed some between pick + claim).
				claimedSet := make(map[string]bool, len(claimed))
				for _, c := range claimed {
					claimedSet[c.ID] = true
				}
				kept := actions[:0]
				for _, a := range actions {
					if claimedSet[a.StoryID] {
						kept = append(kept, a)
					}
				}
				actions = kept
			}

			return json.NewEncoder(os.Stdout).Encode(map[string]any{
				"max":     max,
				"claimed": claim,
				"actions": actions,
			})
		},
	}
	cmd.Flags().IntVar(&max, "max-parallel", 0, "override max parallel slots (else uses config)")
	cmd.Flags().BoolVar(&claim, "claim", true, "atomically claim returned stories (set claimed_at + claimed_by)")
	cmd.Flags().StringVar(&claimer, "claimer", "orchestrator", "claimed_by value (e.g. session id)")
	return cmd
}

// ---------- checkpoint ----------

func newStoryCheckpointCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "checkpoint <story-id>",
		Short: "Evaluate §12.5 dual-trigger; fire a checkpoint row if triggered",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			ctx := context.Background()
			svc, cleanup, err := openStoryService(ctx)
			if err != nil {
				return err
			}
			defer cleanup()

			res, err := svc.EvaluateCheckpoint(ctx, args[0])
			if err != nil {
				return err
			}
			return json.NewEncoder(os.Stdout).Encode(res)
		},
	}
}

// ---------- set-status ----------

func newStorySetStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set-status <story-id> <status>",
		Short: "Admin: directly mutate a story's status",
		Args:  cobra.ExactArgs(2),
		RunE: func(c *cobra.Command, args []string) error {
			ctx := context.Background()
			svc, cleanup, err := openStoryService(ctx)
			if err != nil {
				return err
			}
			defer cleanup()

			id, status := args[0], state.Status(args[1])
			if err := svc.Stories.SetStatus(ctx, id, status); err != nil {
				return fmt.Errorf("set-status %q: %w", id, err)
			}
			fmt.Printf("%s -> %s\n", id, status)
			return nil
		},
	}
}

// ---------- complete ----------

func newStoryCompleteCmd() *cobra.Command {
	var commit, prURL string
	cmd := &cobra.Command{
		Use:   "complete <story-id> [<story-id>...]",
		Short: "Mark stories complete (variadic). --commit/--pr only meaningful for single id",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			ctx := context.Background()
			svc, cleanup, err := openStoryService(ctx)
			if err != nil {
				return err
			}
			defer cleanup()

			if len(args) > 1 && (commit != "" || prURL != "") {
				return fmt.Errorf("--commit / --pr are only meaningful for a single story id")
			}

			for _, id := range args {
				if err := svc.Stories.SetComplete(ctx, id, commit, prURL); err != nil {
					return fmt.Errorf("complete %q: %w", id, err)
				}
				// Release the claim so a future re-plan or admin set-status
				// can re-route the story without claim leftover.
				if err := svc.Stories.ReleaseClaim(ctx, id); err != nil {
					return fmt.Errorf("release claim %q: %w", id, err)
				}
				fmt.Printf("%s -> complete\n", id)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&commit, "commit", "", "commit hash (single-id only)")
	cmd.Flags().StringVar(&prURL, "pr", "", "PR URL (single-id only)")
	return cmd
}

// effectiveMaxParallel mirrors the application-side logic for cmd-level use.
func effectiveMaxParallel(v string) int {
	if v == "" {
		return 4
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return 4
	}
	return n
}

// ---------- applicable-stages ----------

func newStoryApplicableStagesCmd() *cobra.Command {
	var mode string
	cmd := &cobra.Command{
		Use:   "applicable-stages <story-id>",
		Short: "Emit the ordered list of stages the orchestrator must dispatch for this story",
		Long: `Per (story_type × mode), returns:

  - applicable: stages the orchestrator dispatches L3 agents for
  - skipped:    stages auto-skipped (pre-record as blocked-NA)

Use in the orchestrator loop in place of the hardcoded
hydrate→atdd→…→commit list. Saves the subagent token spend of
discovering "this stage is N/A for this story" at runtime.`,
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			ctx := context.Background()
			svc, cleanup, err := openStoryService(ctx)
			if err != nil {
				return err
			}
			defer cleanup()

			st, err := svc.Stories.Get(ctx, args[0])
			if errors.Is(err, state.ErrNotFound) {
				fmt.Fprintf(os.Stderr, "story %q not found\n", args[0])
				os.Exit(1)
			}
			if err != nil {
				return err
			}

			effectiveMode := appstate.Mode(mode)
			if effectiveMode == "" {
				if v, err := svc.Config.Get(ctx, "mode"); err == nil {
					effectiveMode = appstate.Mode(v)
				} else {
					effectiveMode = appstate.ModePragmatic
				}
			}

			applicable := appstate.ApplicableStages(st.StoryType, effectiveMode)
			skipped := appstate.SkippedStages(st.StoryType, effectiveMode)

			applicableStrs := make([]string, 0, len(applicable))
			for _, s := range applicable {
				applicableStrs = append(applicableStrs, string(s))
			}
			skippedStrs := make([]string, 0, len(skipped))
			for _, s := range skipped {
				skippedStrs = append(skippedStrs, string(s))
			}

			return json.NewEncoder(os.Stdout).Encode(map[string]any{
				"story_id":   st.ID,
				"story_type": string(st.StoryType),
				"mode":       string(effectiveMode),
				"applicable": applicableStrs,
				"skipped":    skippedStrs,
			})
		},
	}
	cmd.Flags().StringVar(&mode, "mode", "", "override mode (default: from config.mode)")
	return cmd
}

// ---------- context-bundle ----------

// DefaultStoryContextBundleDir is where renderers look for per-story
// bundles when the cmd's --out flag isn't overridden. Matches what
// the render layer auto-loads.
const DefaultStoryContextBundleDir = "_bmad-output/context-bundles"

func newStoryContextBundleCmd() *cobra.Command {
	var (
		outPath  string
		epicsPath string
		archPath  string
	)
	cmd := &cobra.Command{
		Use:   "context-bundle <story-id>",
		Short: "Pre-extract per-story curated context (entry + FR-matching arch sections) into one file",
		Long: `Deterministic Go extraction — no LLM. Output is a single self-contained
markdown file with the story's lightweight epics.md entry + the
architecture sections matching every FR-Arch-N / FR-NFR-N reference in
that entry.

The render layer auto-loads ` + "`" + `_bmad-output/context-bundles/<story-id>.md` + "`" + `
into stage_hydrate / stage_implement prompts as the .StoryContextBundlePath
slot — agents read this single file instead of grepping the architecture
canon at runtime. Estimated savings: ~40k input tokens per hydrate
dispatch (the agent's "figure out what to read" overhead disappears).

Run per-story before each dispatch:
  bmad story context-bundle <id>

Or batch-pre-build for the next sprint slice ahead of time.`,
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			ctx := context.Background()
			svc, cleanup, err := openStoryService(ctx)
			if err != nil {
				return err
			}
			defer cleanup()

			id := args[0]
			docs, _ := svc.Config.Get(ctx, "docs_folder")
			if epicsPath == "" {
				if docs == "" {
					return fmt.Errorf("context-bundle: --epics required (docs_folder not set)")
				}
				epicsPath = filepath.Join(docs, "epics.md")
			}
			if archPath == "" && docs != "" {
				archPath = filepath.Join(docs, "architecture.md")
			}
			if outPath == "" {
				outPath = filepath.Join(DefaultStoryContextBundleDir, id+".md")
			}

			res, err := appsprint.BuildStoryContext(outPath, appsprint.StoryContextSources{
				StoryID:          id,
				EpicsPath:        epicsPath,
				ArchitecturePath: archPath,
			})
			if err != nil {
				return err
			}
			return json.NewEncoder(os.Stdout).Encode(res)
		},
	}
	cmd.Flags().StringVar(&outPath, "out", "", "output path (default: _bmad-output/context-bundles/<story-id>.md)")
	cmd.Flags().StringVar(&epicsPath, "epics", "", "epics.md path (default: <docs_folder>/epics.md)")
	cmd.Flags().StringVar(&archPath, "arch", "", "architecture.md path (default: <docs_folder>/architecture.md)")
	return cmd
}

// ---------- set-type ----------

func newStorySetTypeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set-type <story-id> <code|doc|mixed>",
		Short: "Override a story's story_type (drives stage-applicability matrix)",
		Args:  cobra.ExactArgs(2),
		RunE: func(c *cobra.Command, args []string) error {
			ctx := context.Background()
			svc, cleanup, err := openStoryService(ctx)
			if err != nil {
				return err
			}
			defer cleanup()

			id := args[0]
			storyType := state.StoryType(args[1])
			if storyType != state.StoryTypeCode &&
				storyType != state.StoryTypeDoc &&
				storyType != state.StoryTypeMixed {
				return fmt.Errorf("invalid story_type %q (want code|doc|mixed)", args[1])
			}
			st, err := svc.Stories.Get(ctx, id)
			if err != nil {
				return err
			}
			st.StoryType = storyType
			if err := svc.Stories.Update(ctx, st); err != nil {
				return err
			}
			fmt.Printf("%s story_type -> %s\n", id, storyType)
			return nil
		},
	}
}
