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
	"github.com/sosalejandro/bmad-story-runner-cli/infrastructure"
	"github.com/sosalejandro/bmad-story-runner-cli/infrastructure/state/sqlite"
)

// storyCompleteGitRunner is the GitRunner used by `bmad story complete
// --commit`. Production wiring is ExecGitRunner; tests swap in a mock.
// Package-level so the unit test can inject without exporting the full
// command constructor surface area.
var storyCompleteGitRunner infrastructure.GitRunner = infrastructure.ExecGitRunner{}

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
		scopeFlag  string
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
				if jsonOutput {
					return emitStoryDetailJSON(ctx, svc, c, args[0])
				}
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
			if jsonOutput {
				return emitStoryListJSON(ctx, svc, c, f, statusFlag, stageFlag, scopeFlag, hasEnvSet, hasEnv)
			}
			return printStoryTable(ctx, svc, f, scopeFlag)
		},
	}
	cmd.Flags().StringVar(&statusFlag, "status", "", "filter by status")
	cmd.Flags().StringVar(&stageFlag, "stage", "", "filter by current stage")
	cmd.Flags().StringVar(&scopeFlag, "scope", "", "restrict rows to one epic id (e.g. 2 → only 2.* stories)")
	cmd.Flags().BoolVar(&hasEnv, "has-env", false, "filter by env-up state")
	return cmd
}

func printStoryTable(ctx context.Context, svc *appstate.StoryService, f state.StoryFilter, scope string) error {
	rows, err := svc.Stories.List(ctx, f)
	if err != nil {
		return err
	}
	rows = filterStoriesByScope(rows, scope)
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

// filterStoriesByScope is the `bmad story status --scope <epic>` post-filter
// (issue #35). Empty scope is a no-op. Uses the same prefix+dot matching
// rule as `bmad sprint plan --scope` via appsprint.StoryMatchesEpic, so
// --scope 1 will NOT match 10.* (avoids the off-by-one prefix bug).
func filterStoriesByScope(rows []state.Story, scope string) []state.Story {
	if scope == "" {
		return rows
	}
	out := rows[:0]
	for _, st := range rows {
		if appsprint.StoryMatchesEpic(st.ID, scope) {
			out = append(out, st)
		}
	}
	return out
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
			result := map[string]string{
				"action":   "dispatch",
				"agent":    "story-hydrator",
				"story_id": id,
				"stage":    string(stage),
			}
			if jsonOutput {
				return emitJSONStdout(commandPathSansRoot(c),
					map[string]any{"story_id": id}, result, nil)
			}
			return json.NewEncoder(os.Stdout).Encode(result)
		},
	}
}

// ---------- next ----------

// newStoryNextCmd is the canonical `bmad story next` command. The same
// command body is also exposed at the root as `bmad next-actions` via
// newNextActionsAliasCmd — both call buildNextCmd with their own Use
// string so flag-bound state isn't shared across the two registrations.
func newStoryNextCmd() *cobra.Command {
	return buildNextCmd("next")
}

// newNextActionsAliasCmd is the top-level `bmad next-actions` alias
// (issue #71 sub-issue 1). The orchestrator skill documents this verb
// throughout its sprint loop spec; the real command lives at
// `bmad story next`. This alias preserves the entire flag surface
// (--max-parallel, --claim, --claimer, --scope, --no-hydration-priority)
// so skill docs referencing `bmad next-actions` resolve at runtime.
func newNextActionsAliasCmd() *cobra.Command {
	cmd := buildNextCmd("next-actions")
	cmd.Short = "Alias of `bmad story next` — emit a parallel-eligible next-action batch"
	// Persistent --state flag normally lives on the story parent; surface
	// it here too so `bmad next-actions --state ./bmad-state.db` works.
	addV6PersistentFlags(cmd)
	return cmd
}

func buildNextCmd(use string) *cobra.Command {
	var (
		max                 int
		claim               bool
		claimer             string
		scope               string
		noHydrationPriority bool
	)
	cmd := &cobra.Command{
		Use:   use,
		Short: "Emit a parallel-eligible next-action batch",
		Long: `By default emits candidates WITHOUT mutating state (--claim=false).
With --claim, atomically marks the picked stories claimed_at + claimed_by
in a single transaction so two orchestrator iterations cannot both pick the
same story. Always pass --claim in production orchestrator loops — the only
no-claim use case is read-only inspection.

Stale-claim reaping (issue #21 gap 3): before scanning candidates, any
claim older than config.claim_ttl_seconds (default 600s) on a
non-complete story is cleared so a crashed orchestrator session doesn't
permanently lock the story. Reaped ids print a one-line warning to
stderr; the JSON envelope on stdout lists them in reaped_claims so
downstream telemetry can count them. Set claim_ttl_seconds=0 in config
to disable the reaper.

--scope <epic-id> (issue #35): per-call filter that restricts the
candidate set to stories whose id matches the epic (e.g. --scope 2 →
only 2.* stories; --scope 10 will NOT match 1.* — uses the same
prefix+dot rule as ` + "`bmad sprint plan --scope`" + `). The filter is
applied to candidates BEFORE pick, never mutates state, and an empty
match returns an empty batch (exit 0), not an error.

Hydration-priority sort (issue #49): by default, candidates whose
hydrated_file is non-nil drain BEFORE fresh stories — preserving the
~80-100k token hydrate cost across orchestrator session restarts
(skill updates, sprint checkpoints, worktree restores). The
dep-order + id-ordinal tiebreaker is preserved within each bucket.
Pass --no-hydration-priority to revert to the legacy dep-order-only
behaviour (the rare "I explicitly want fresh now" override).`,
		RunE: func(c *cobra.Command, args []string) error {
			ctx := context.Background()
			svc, cleanup, err := openStoryService(ctx)
			if err != nil {
				return err
			}
			defer cleanup()

			// Issue #21 gap 3 (b): reap stale claims first so a stuck story
			// becomes eligible on the same `bmad story next` invocation
			// that exposes the gap. Operator gets a warning per reaped id —
			// silence here would mask the orchestrator-crash residue.
			//
			// NOTE: reaping is intentionally global, not --scope-bounded —
			// a stuck claim outside the requested scope is still crash
			// residue worth clearing on the same invocation. The reaper
			// touches claim metadata only, never story status.
			ttl := effectiveClaimTTL(ctx, svc)
			reaped, err := svc.Stories.ReapStaleClaims(ctx, ttl)
			if err != nil {
				return fmt.Errorf("story next: reap stale claims: %w", err)
			}
			for _, id := range reaped {
				fmt.Fprintf(os.Stderr,
					"WARN: reaped stale claim on %s (age > %ds; assuming orchestrator crash)\n",
					id, ttl)
			}

			actions, err := svc.NextWithOptions(ctx, max, appstate.NextOptions{
				Scope:               scope,
				NoHydrationPriority: noHydrationPriority,
			})
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

			result := map[string]any{
				"max":               max,
				"claimed":           claim,
				"actions":           actions,
				"reaped_claims":     reaped,
				"claim_ttl_seconds": ttl,
			}
			if scope != "" {
				result["scope"] = scope
			}
			if jsonOutput {
				args := map[string]any{
					"max_parallel": max,
					"claim":        claim,
					"claimer":      claimer,
				}
				if scope != "" {
					args["scope"] = scope
				}
				warnings := make([]string, 0, len(reaped))
				for _, id := range reaped {
					warnings = append(warnings,
						fmt.Sprintf("reaped stale claim on %s (age > %ds; assuming orchestrator crash)", id, ttl))
				}
				return emitJSONStdout(commandPathSansRoot(c), args, result, warnings)
			}
			return json.NewEncoder(os.Stdout).Encode(result)
		},
	}
	cmd.Flags().IntVar(&max, "max-parallel", 0, "override max parallel slots (else uses config)")
	cmd.Flags().BoolVar(&claim, "claim", true, "atomically claim returned stories (set claimed_at + claimed_by)")
	cmd.Flags().StringVar(&claimer, "claimer", "orchestrator", "claimed_by value (e.g. session id)")
	cmd.Flags().StringVar(&scope, "scope", "", "restrict candidates to one epic id (e.g. 2 → only 2.* stories; empty = no filter)")
	cmd.Flags().BoolVar(&noHydrationPriority, "no-hydration-priority", false,
		"opt out of issue-#49 hydration-priority bucket sort (default: hydrated-pending stories drain first to avoid re-paying ~80-100k token hydrate cost on session restarts)")
	return cmd
}

// DefaultClaimTTLSeconds is used when config.claim_ttl_seconds is unset.
// 10 minutes is conservative enough to not race a slow but still-running
// dispatch, yet short enough to reap quickly after a real crash.
const DefaultClaimTTLSeconds = 600

// effectiveClaimTTL reads config.claim_ttl_seconds. Returns
// DefaultClaimTTLSeconds when unset or unparseable. Returns 0 (which
// disables the reaper) only when the config row is explicitly "0".
func effectiveClaimTTL(ctx context.Context, svc *appstate.StoryService) int {
	v, err := svc.Config.Get(ctx, "claim_ttl_seconds")
	if err != nil {
		return DefaultClaimTTLSeconds
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return DefaultClaimTTLSeconds
	}
	if n < 0 {
		return 0
	}
	return n
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
			// Look up the prior status so the JSON envelope can include
			// it as old_status. We tolerate not-found here (returns "") —
			// SetStatus will surface the real error if the story really
			// doesn't exist.
			var oldStatus string
			if pre, err := svc.Stories.Get(ctx, id); err == nil {
				oldStatus = string(pre.Status)
			}
			if err := svc.Stories.SetStatus(ctx, id, status); err != nil {
				return fmt.Errorf("set-status %q: %w", id, err)
			}
			if jsonOutput {
				return emitJSONStdout(commandPathSansRoot(c),
					map[string]any{"story_id": id, "new_status": string(status)},
					map[string]any{
						"ok":         true,
						"story_id":   id,
						"old_status": oldStatus,
						"new_status": string(status),
					}, nil)
			}
			fmt.Printf("%s -> %s\n", id, status)
			return nil
		},
	}
}

// ---------- complete ----------

func newStoryCompleteCmd() *cobra.Command {
	var (
		commitHash string
		prURL      string
		autoCommit bool
		noCommit   bool
		repoDir    string
	)
	cmd := &cobra.Command{
		Use:   "complete <story-id> [<story-id>...]",
		Short: "Mark stories complete (variadic). --commit-hash/--pr-url metadata; --commit auto-commits the story file",
		Long: `Mark one or more stories complete.

Metadata flags (single-id only):
  --commit-hash <sha>   record the commit that finished the work
  --pr-url <url>        record the PR that landed the work

Compound workflow flag (single-id only):
  --commit              after setting status=complete, also:
                          1. patch the story .md file's **Status:** line
                          2. git add the story file
                          3. git commit with a generated message
                        Fails if the working tree has unrelated changes
                        (commit those separately or pass --no-commit).
  --no-commit           explicitly disables the compound workflow even
                        when --commit is also set (escape hatch for
                        scripts that want metadata-only behaviour on a
                        clean tree).

Idempotency: re-running --commit on an already-complete story is a
no-op; a warning is emitted to stderr and the command returns ok.`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			ctx := context.Background()
			svc, cleanup, err := openStoryService(ctx)
			if err != nil {
				return err
			}
			defer cleanup()

			singleOnly := commitHash != "" || prURL != "" || autoCommit
			if len(args) > 1 && singleOnly {
				return fmt.Errorf("--commit / --commit-hash / --pr-url are only meaningful for a single story id")
			}

			completed := make([]string, 0, len(args))
			for _, id := range args {
				if err := runCompleteOne(ctx, svc, id, commitHash, prURL, autoCommit, noCommit, repoDir); err != nil {
					return err
				}
				completed = append(completed, id)
			}
			if jsonOutput {
				return emitJSONStdout(commandPathSansRoot(c),
					map[string]any{"story_ids": args},
					map[string]any{
						"ok":          true,
						"completed":   completed,
						"commit_hash": commitHash,
						"pr":          prURL,
					}, nil)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&commitHash, "commit-hash", "", "commit hash metadata (single-id only)")
	cmd.Flags().StringVar(&prURL, "pr-url", "", "PR URL metadata (single-id only)")
	cmd.Flags().BoolVar(&autoCommit, "commit", false, "after marking complete, patch the story file + git commit (single-id only)")
	cmd.Flags().BoolVar(&noCommit, "no-commit", false, "override: disable the --commit compound workflow even if --commit is set")
	cmd.Flags().StringVar(&repoDir, "repo-dir", ".", "repository directory for the --commit git operations (default: cwd)")
	return cmd
}

// runCompleteOne handles one story id. Split out so the compound flow
// stays readable + so the unit test can hit it directly without
// constructing the cobra command tree.
func runCompleteOne(
	ctx context.Context,
	svc *appstate.StoryService,
	id, commitHash, prURL string,
	autoCommit, noCommit bool,
	repoDir string,
) error {
	// Idempotency check for the compound flow — re-running --commit on a
	// story already in `complete` status is a no-op, not an error. The
	// metadata-only flow is allowed to re-set fields freely (SetComplete
	// already handles that case at the repo layer).
	if autoCommit && !noCommit {
		st, err := svc.Stories.Get(ctx, id)
		if err != nil {
			return fmt.Errorf("complete %q: %w", id, err)
		}
		if st.Status == state.StatusComplete {
			fmt.Fprintf(os.Stderr, "WARN: story %s already complete; --commit is a no-op\n", id)
			return nil
		}
	}

	if err := svc.Stories.SetComplete(ctx, id, commitHash, prURL); err != nil {
		return fmt.Errorf("complete %q: %w", id, err)
	}
	if err := svc.Stories.ReleaseClaim(ctx, id); err != nil {
		return fmt.Errorf("release claim %q: %w", id, err)
	}
	if !jsonOutput {
		fmt.Printf("%s -> complete\n", id)
	}

	if !autoCommit || noCommit {
		return nil
	}

	st, err := svc.Stories.Get(ctx, id)
	if err != nil {
		return fmt.Errorf("complete --commit %q: refetch: %w", id, err)
	}
	if st.File == "" {
		return fmt.Errorf("complete --commit %q: story.file is empty; cannot patch story .md", id)
	}

	// Step 1: patch the story file's Status: line. The patcher is
	// idempotent — running it twice produces identical content.
	patcher := infrastructure.NewMDStoryFilePatcher(log)
	if err := patcher.PatchStatus(st.File, string(state.StatusComplete)); err != nil {
		return fmt.Errorf("complete --commit %q: patch story file: %w", id, err)
	}

	// Step 2: dirty-tree check. The story file path stored in the DB is
	// either absolute or repo-relative; for the porcelain comparison
	// we want the path git itself would emit, which is the repo-relative
	// form. If the stored File is absolute, derive the relative form
	// against repoDir.
	storyFileRel := st.File
	if filepath.IsAbs(st.File) {
		if absRepo, err := filepath.Abs(repoDir); err == nil {
			if rel, err := filepath.Rel(absRepo, st.File); err == nil {
				storyFileRel = filepath.ToSlash(rel)
			}
		}
	}

	porcelain, err := storyCompleteGitRunner.Status(ctx, repoDir)
	if err != nil {
		return fmt.Errorf("complete --commit %q: git status: %w", id, err)
	}
	if clean, reason := infrastructure.IsCleanForStoryFile(porcelain, storyFileRel); !clean {
		return fmt.Errorf(
			"complete --commit %q: working tree dirty (%s); commit your other changes first or rerun with --no-commit",
			id, reason)
	}

	// Step 3: stage the story file + commit.
	if err := storyCompleteGitRunner.Add(ctx, repoDir, storyFileRel); err != nil {
		return fmt.Errorf("complete --commit %q: %w", id, err)
	}
	msg := fmt.Sprintf("chore(story): mark %s complete\n\nBMAD-Story: %s", id, id)
	sha, err := storyCompleteGitRunner.Commit(ctx, repoDir, msg)
	if err != nil {
		return fmt.Errorf("complete --commit %q: %w", id, err)
	}
	if !jsonOutput {
		if sha != "" {
			fmt.Printf("%s -> committed %s\n", id, sha)
		} else {
			fmt.Printf("%s -> committed\n", id)
		}
	}
	return nil
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

			result := map[string]any{
				"story_id":   st.ID,
				"story_type": string(st.StoryType),
				"mode":       string(effectiveMode),
				"applicable": applicableStrs,
				"skipped":    skippedStrs,
			}
			if jsonOutput {
				return emitJSONStdout(commandPathSansRoot(c),
					map[string]any{"story_id": st.ID, "mode": string(effectiveMode)},
					result, nil)
			}
			return json.NewEncoder(os.Stdout).Encode(result)
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
		outPath   string
		epicsPath string
		archPath  string
		repoRoot  string
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
			if repoRoot == "" {
				// Default to the bmad CLI cwd — that's where dispatches
				// already run from, so atlas's codeindex scan sees the
				// same source tree as the rest of the pipeline.
				cwd, err := os.Getwd()
				if err != nil {
					return fmt.Errorf("context-bundle: resolve cwd for repo root: %w", err)
				}
				repoRoot = cwd
			}

			res, err := appsprint.BuildStoryContext(outPath, appsprint.StoryContextSources{
				StoryID:          id,
				EpicsPath:        epicsPath,
				ArchitecturePath: archPath,
				RepoRoot:         repoRoot,
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
	cmd.Flags().StringVar(&repoRoot, "repo-root", "", "project root for atlas codeindex scan (default: cwd; only used when BMAD_CONTEXT_BUNDLE_INCLUDE_ATLAS=1)")
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
