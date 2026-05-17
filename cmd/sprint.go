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
	)
	addV6PersistentFlags(cmd)
	return cmd
}

// ---------- plan ----------

func newSprintPlanCmd() *cobra.Command {
	var (
		epicsPath string
		maxP      int
	)
	cmd := &cobra.Command{
		Use:   "plan",
		Short: "Parse epics.md → ingest stories + dependencies + build batches",
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
			parsed, err := appsprint.ParseEpicsFile(epicsPath)
			if err != nil {
				return err
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
			res, err := planner.Plan(ctx, parsed, maxP)
			if err != nil {
				return err
			}
			return json.NewEncoder(os.Stdout).Encode(res)
		},
	}
	cmd.Flags().StringVar(&epicsPath, "epics", "", "epics.md path (default: <docs_folder>/epics.md)")
	cmd.Flags().IntVar(&maxP, "max-parallel", 0, "override max parallel slots per batch")
	return cmd
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
			fmt.Println("sprint paused (next iteration will exit gracefully)")
			return nil
		},
	}
}

// ---------- resume ----------

func newSprintResumeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "resume",
		Short: "Clear sprint.paused; resume orchestrator on next invocation",
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
			fmt.Println("sprint resumed")
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

			unresolved, _ := checkpoints.Unresolved(ctx)
			paused, _ := cfg.Get(ctx, sprintPausedKey)
			mode, _ := cfg.Get(ctx, "mode")

			report := map[string]any{
				"mode":             mode,
				"paused":           paused == "true",
				"total_stories":    len(all),
				"by_status":        counts,
				"batches":          batchSummary,
				"unresolved_checkpoint": unresolved,
				"tokens":           tokens,
			}
			if raw {
				return json.NewEncoder(os.Stdout).Encode(report)
			}
			return printSprintTable(report, counts, batchSummary, tokens)
		},
	}
	cmd.Flags().BoolVar(&raw, "raw", false, "emit raw JSON")
	return cmd
}

func printSprintTable(report map[string]any, counts, batches map[string]int, tokens state.TokenCounts) error {
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
	if cp, ok := report["unresolved_checkpoint"].(*state.Checkpoint); ok && cp != nil {
		fmt.Fprintln(w, "")
		fmt.Fprintln(w, "UNRESOLVED CHECKPOINT — run `bmad sprint resume` after deciding")
	}
	return w.Flush()
}

// ensure ignored import surfaces a build error if unused upstream
var _ = strings.HasPrefix
var _ = errors.Is
