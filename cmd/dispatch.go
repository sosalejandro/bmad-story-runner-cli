package cmd

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/sosalejandro/bmad-story-runner-cli/domain/state"
	"github.com/sosalejandro/bmad-story-runner-cli/infrastructure/state/sqlite"
)

// newDispatchCmd is the `bmad dispatch <verb>` namespace.
//
// Lifecycle: `begin` pre-records a dispatch row with status=dispatched and a
// generated idempotency key, BEFORE the orchestrator invokes Task(). The
// key is embedded in the rendered prompt; the L3 agent echoes it back in
// its return JSON; `record --key <k>` then updates the same row by key.
// Crash between begin and record → InFlight() picks up the orphan row for
// reconciliation.
func newDispatchCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dispatch <verb>",
		Short: "Record L3 agent dispatch results (token cost + idempotency per §12.7)",
	}
	cmd.AddCommand(
		newDispatchBeginCmd(),
		newDispatchRecordCmd(),
		newDispatchInFlightCmd(),
	)
	addV6PersistentFlags(cmd)
	return cmd
}

// ---------- begin (pre-record + emit idempotency key) ----------

func newDispatchBeginCmd() *cobra.Command {
	var (
		agentRole string
		attemptNo int
	)
	cmd := &cobra.Command{
		Use:   "begin <story-id> <stage>",
		Short: "Pre-record a dispatch row (status=dispatched) + return its idempotency key",
		Long: `Generates a UUID idempotency key, inserts a dispatch row with status=
dispatched and returned_at=NULL, and prints the key to stdout (json).

Workflow:
  key=$(bmad dispatch begin 1.1 implement | jq -r .idempotency_key)
  prompt=$(bmad render stage_implement --story 1.1 --idempotency-key "$key")
  result=$(<dispatch via claude code Task tool with $prompt>)
  bmad dispatch record --key "$key" --status ok --input-tokens N ...

The L3 agent's prompt should instruct it to echo the key back so the
orchestrator can confirm the dispatch it's recording matches the
intended one. The UNIQUE index ensures double-record is impossible.`,
		Args: cobra.ExactArgs(2),
		RunE: func(c *cobra.Command, args []string) error {
			ctx := context.Background()
			db, err := openV6DB(ctx)
			if err != nil {
				return err
			}
			defer db.Close()

			storyID := args[0]
			stage := state.Stage(args[1])
			role := agentRole
			if role == "" {
				role = agentRoleForStage(stage)
			}

			key := newIdempotencyKey()
			d := state.Dispatch{
				StoryID:        storyID,
				Stage:          stage,
				AgentRole:      role,
				AttemptNo:      attemptNo,
				Status:         state.DispatchDispatched,
				IdempotencyKey: &key,
			}
			id, err := sqlite.NewDispatchesStore(db).Insert(ctx, d)
			if err != nil {
				return fmt.Errorf("dispatch begin: %w", err)
			}
			return json.NewEncoder(os.Stdout).Encode(map[string]any{
				"dispatch_id":      id,
				"idempotency_key":  key,
				"story_id":         storyID,
				"stage":            string(stage),
				"agent_role":       role,
				"attempt":          attemptNo,
				"status":           string(state.DispatchDispatched),
			})
		},
	}
	cmd.Flags().StringVar(&agentRole, "agent", "", "agent role (default: derived from stage)")
	cmd.Flags().IntVar(&attemptNo, "attempt", 1, "attempt number (1 for first try)")
	return cmd
}

// newIdempotencyKey generates a uuid-v4-like key without importing a uuid
// library. 128 bits from crypto/rand; collision risk effectively zero.
func newIdempotencyKey() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// rand.Read failing is unrecoverable; surface as panic since we
		// can't safely generate keys.
		panic(fmt.Sprintf("crypto/rand failed: %v", err))
	}
	// Set v4 + variant bits (RFC 4122 cosmetic; functionally any 128 bits work).
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// ---------- in-flight (crash recovery diagnostic) ----------

func newDispatchInFlightCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "in-flight",
		Short: "List dispatches with status=dispatched + returned_at IS NULL (crash recovery)",
		RunE: func(c *cobra.Command, args []string) error {
			ctx := context.Background()
			db, err := openV6DB(ctx)
			if err != nil {
				return err
			}
			defer db.Close()
			rows, err := sqlite.NewDispatchesStore(db).InFlight(ctx)
			if err != nil {
				return err
			}
			return json.NewEncoder(os.Stdout).Encode(rows)
		},
	}
}

// ---------- record ----------

func newDispatchRecordCmd() *cobra.Command {
	var (
		agentRole             string
		attemptNo             int
		reason                string
		inputTokens           int64
		outputTokens          int64
		cacheRead             int64
		cacheCreate           int64
		totalTokens           int64
		model                 string
		durationMS            int64
		idemKey               string
		hydratedFile          string
		overwriteHydratedFile bool
	)
	cmd := &cobra.Command{
		Use:   "record [<story-id> <stage> <status>]",
		Short: "Record an L3 invocation's return — by --key (preferred) or by positional",
		Long: `Two forms:

  bmad dispatch record --key <k> --status ok --input-tokens N ...
    Updates the dispatch row created by ` + "`begin`" + ` (matching idempotency
    key). Preferred — closes the loop and is replay-safe.

  bmad dispatch record <story-id> <stage> <status>
    Legacy form that inserts a fresh row without key. Useful for
    one-off records (e.g. manual entries); does NOT participate in
    crash-recovery reconciliation.

Token accounting (issue #15): pass the four breakdown axes
(--input-tokens, --output-tokens, --cache-read-tokens,
--cache-create-tokens) when the subagent's <usage> block provides
them — this is what powers the cache_hit_rate column in
` + "`bmad sprint status`" + `. ` + "`--total-tokens`" + ` is kept for backward compat:
provide either form, or both (the latter validates the sum).

Hydrated-file recording (issue #21 gap 2): when ` + "`--stage hydrate`" + ` succeeds,
pass ` + "`--hydrated-file <path>`" + ` to write stories.hydrated_file in the same
operation. The L1 orchestrator no longer has to touch SQLite directly to
satisfy the .HydratedFile slot in stage_atdd / stage_implement templates.
By default the write refuses to clobber a non-empty existing value (the
prior hydrate's output); pass ` + "`--overwrite-hydrated-file`" + ` to opt in to
replacement. Validation: ` + "`--hydrated-file`" + ` is only honored when
stage=hydrate AND status=ok.`,
		Args: cobra.MaximumNArgs(3),
		RunE: func(c *cobra.Command, args []string) error {
			ctx := context.Background()
			db, err := openV6DB(ctx)
			if err != nil {
				return err
			}
			defer db.Close()
			store := sqlite.NewDispatchesStore(db)
			stories := sqlite.NewStoriesStore(db)
			now := time.Now().UTC()

			// Issue #15: reconcile --total-tokens with the 4-axis breakdown.
			totalProvided := c.Flags().Changed("total-tokens")
			breakdownProvided := c.Flags().Changed("input-tokens") ||
				c.Flags().Changed("output-tokens") ||
				c.Flags().Changed("cache-read-tokens") ||
				c.Flags().Changed("cache-create-tokens")
			reconciled, rerr := reconcileTokenInputs(
				totalProvided, breakdownProvided,
				totalTokens, inputTokens, outputTokens, cacheRead, cacheCreate,
			)
			if rerr != nil {
				return fmt.Errorf("dispatch record: %w", rerr)
			}
			inputTokens = reconciled.Input
			outputTokens = reconciled.Output
			cacheRead = reconciled.CacheRead
			cacheCreate = reconciled.CacheCreate

			// Key-based form (preferred).
			if idemKey != "" {
				statusFlag, _ := c.Flags().GetString("status")
				if statusFlag == "" {
					return fmt.Errorf("--key requires --status (ok|blocked|errored)")
				}
				status := state.DispatchStatus(statusFlag)
				if status != state.DispatchOK && status != state.DispatchBlocked && status != state.DispatchErrored {
					return fmt.Errorf("invalid --status %q (want ok|blocked|errored)", statusFlag)
				}
				if err := store.MarkReturnedByKey(ctx, idemKey, status, reason,
					state.TokenCounts{Input: inputTokens, Output: outputTokens, CacheRead: cacheRead, CacheCreate: cacheCreate},
					model, durationMS, now,
				); err != nil {
					if errors.Is(err, state.ErrNotFound) {
						return fmt.Errorf("dispatch record: no row matches idempotency key %q (already recorded? wrong key?)", idemKey)
					}
					return err
				}
				// Look up the row once for any post-record state work that
				// needs story_id / stage (hydrated-file + claim release).
				var (
					recorded   state.Dispatch
					recordedOK bool
				)
				if hydratedFile != "" || status == state.DispatchOK {
					d, gerr := store.GetByKey(ctx, idemKey)
					if gerr != nil {
						// Lookup failure for the post-record bookkeeping is
						// not fatal — the dispatch row IS recorded.
						// Print a warning so an operator notices.
						fmt.Fprintf(os.Stderr,
							"WARN: dispatch %s recorded by key but follow-up lookup failed: %v\n",
							idemKey, gerr)
					} else {
						recorded = d
						recordedOK = true
					}
				}
				// Issue #21 gap 2: write stories.hydrated_file when this is
				// a successful hydrate dispatch.
				if hydratedFile != "" && recordedOK {
					if err := applyHydratedFile(ctx, stories, recorded.StoryID, recorded.Stage, status, hydratedFile, overwriteHydratedFile); err != nil {
						return err
					}
				}
				// Issue #21 gap 3 (a): auto-release the claim on success.
				// Without this, crashed orchestrator sessions that never
				// reached `bmad story complete` left stories permanently
				// claimed.
				if recordedOK {
					if err := releaseClaimOnOK(ctx, stories, recorded.StoryID, status); err != nil {
						// Release failure isn't fatal — the dispatch row IS
						// recorded. Surface as a warning so the operator
						// notices the stale claim and reaps manually if
						// needed.
						fmt.Fprintf(os.Stderr, "WARN: %v\n", err)
					}
				}
				fmt.Printf("dispatch returned by key=%s: %s (tokens %d:%d:%d:%d, %dms)\n",
					idemKey, status,
					inputTokens, outputTokens, cacheRead, cacheCreate, durationMS)
				return nil
			}

			// Legacy positional form.
			if len(args) != 3 {
				return fmt.Errorf("either --key + --status, or 3 positional args (story-id, stage, status) required")
			}
			storyID := args[0]
			stage := state.Stage(args[1])
			status := state.DispatchStatus(args[2])
			if status != state.DispatchOK && status != state.DispatchBlocked && status != state.DispatchErrored {
				return fmt.Errorf("invalid status %q (want ok|blocked|errored)", args[2])
			}
			role := agentRole
			if role == "" {
				role = agentRoleForStage(stage)
			}

			d := state.Dispatch{
				StoryID:    storyID,
				Stage:      stage,
				AgentRole:  role,
				AttemptNo:  attemptNo,
				Status:     status,
				Tokens:     state.TokenCounts{Input: inputTokens, Output: outputTokens, CacheRead: cacheRead, CacheCreate: cacheCreate},
				DurationMS: durationMS,
				ReturnedAt: &now,
			}
			if reason != "" {
				d.Reason = &reason
			}
			if model != "" {
				d.Model = &model
			}

			id, err := store.Insert(ctx, d)
			if err != nil {
				return err
			}
			// Issue #21 gap 2: same hydrated-file flow for the positional form.
			if hydratedFile != "" {
				if err := applyHydratedFile(ctx, stories, storyID, stage, status, hydratedFile, overwriteHydratedFile); err != nil {
					return err
				}
			}
			// Issue #21 gap 3 (a): auto-release the claim on success.
			if err := releaseClaimOnOK(ctx, stories, storyID, status); err != nil {
				fmt.Fprintf(os.Stderr, "WARN: %v\n", err)
			}
			fmt.Printf("dispatch %d recorded: %s/%s/%s (tokens %d:%d:%d:%d, %dms)\n",
				id, storyID, stage, status,
				inputTokens, outputTokens, cacheRead, cacheCreate, durationMS)
			return nil
		},
	}
	cmd.Flags().StringVar(&idemKey, "key", "", "idempotency key from `dispatch begin` (preferred)")
	cmd.Flags().String("status", "", "ok|blocked|errored (required with --key)")
	cmd.Flags().StringVar(&agentRole, "agent", "", "agent role (default: derived from stage)")
	cmd.Flags().IntVar(&attemptNo, "attempt", 1, "attempt number (1 for first try)")
	cmd.Flags().StringVar(&reason, "reason", "", "blocking/erroring reason")
	cmd.Flags().Int64Var(&inputTokens, "input-tokens", 0, "input token count")
	cmd.Flags().Int64Var(&outputTokens, "output-tokens", 0, "output token count")
	cmd.Flags().Int64Var(&cacheRead, "cache-read-tokens", 0, "cache-read token count")
	cmd.Flags().Int64Var(&cacheCreate, "cache-create-tokens", 0, "cache-create token count")
	cmd.Flags().Int64Var(&totalTokens, "total-tokens", 0, "deprecated: total tokens (use the 4 breakdown flags instead; issue #15)")
	cmd.Flags().StringVar(&model, "model", "", "model identifier")
	cmd.Flags().Int64Var(&durationMS, "duration-ms", 0, "dispatch wall-clock duration in ms")
	cmd.Flags().StringVar(&hydratedFile, "hydrated-file", "", "write stories.hydrated_file when --stage hydrate succeeds (issue #21 gap 2)")
	cmd.Flags().BoolVar(&overwriteHydratedFile, "overwrite-hydrated-file", false, "allow --hydrated-file to overwrite a prior non-empty value (default: refuse)")
	return cmd
}

// applyHydratedFile guards the --hydrated-file flag's gates and writes the
// stories.hydrated_file column. Used by both the key-based and positional
// branches of `bmad dispatch record` (issue #21 gap 2).
//
// Validation:
//   - stage MUST be "hydrate" — recording a non-hydrate stage with
//     --hydrated-file is a user error (the flag wouldn't have produced a
//     useful row anywhere else).
//   - status MUST be "ok" — only successful hydrate dispatches yield a
//     real path. Blocked/errored hydrates leave hydrated_file untouched.
func applyHydratedFile(
	ctx context.Context,
	stories interface {
		SetHydratedFile(ctx context.Context, id, hydratedFile string, overwrite bool) error
	},
	storyID string,
	stage state.Stage,
	status state.DispatchStatus,
	hydratedFile string,
	overwrite bool,
) error {
	if stage != state.StageHydrate {
		return fmt.Errorf("dispatch record: --hydrated-file only valid with --stage hydrate (got %q)", stage)
	}
	if status != state.DispatchOK {
		return fmt.Errorf("dispatch record: --hydrated-file requires --status ok (got %q)", status)
	}
	if err := stories.SetHydratedFile(ctx, storyID, hydratedFile, overwrite); err != nil {
		if errors.Is(err, state.ErrAlreadySet) {
			return fmt.Errorf("dispatch record: stories.hydrated_file already populated for %q; pass --overwrite-hydrated-file to replace", storyID)
		}
		return fmt.Errorf("dispatch record: set hydrated_file: %w", err)
	}
	return nil
}

// releaseClaimOnOK is the seam for issue #21 gap 3 (a): a successful
// dispatch frees its claim so the next orchestrator iteration is free to
// pick the story again (or move it to the next stage). Idempotent — the
// underlying ReleaseClaim is safe to call on an already-clear row.
// Returns nil even when status != ok (the caller always invokes it; the
// gate lives here so the cmd body stays linear).
//
// Defensive: a ReleaseClaim error never blocks the dispatch from being
// considered "recorded" — we WARN instead, because the row is already in
// SQLite and rolling back the claim release would be the inverse of the
// intent (leaving a stale claim).
func releaseClaimOnOK(
	ctx context.Context,
	stories interface {
		ReleaseClaim(ctx context.Context, id string) error
	},
	storyID string,
	status state.DispatchStatus,
) error {
	if status != state.DispatchOK {
		return nil
	}
	if err := stories.ReleaseClaim(ctx, storyID); err != nil {
		return fmt.Errorf("dispatch record: release claim for %q: %w", storyID, err)
	}
	return nil
}

// cacheHitRate returns cache_read / (input + cache_read) as a percentage
// (0..100, float64). Returns 0 when the denominator is 0.
func cacheHitRate(t state.TokenCounts) float64 {
	denom := t.Input + t.CacheRead
	if denom <= 0 {
		return 0
	}
	return float64(t.CacheRead) / float64(denom) * 100.0
}

// reconcileTokenInputs decides what (input, output, cache-read, cache-create)
// to persist given the flags the caller actually set.
//
// Issue #15: --total-tokens stays available for backward compat. Legacy
// callers that pass only --total-tokens get their value bucketed into
// input_tokens so cumulative dashboards still reflect cost; cache_hit_rate
// reads 0 until they upgrade to the breakdown flags. If both forms are
// provided, the sum MUST match — that's the contract the L1 orchestrator
// relies on to confirm its <usage> parse is correct.
func reconcileTokenInputs(
	totalProvided, breakdownProvided bool,
	total, input, output, cacheRead, cacheCreate int64,
) (state.TokenCounts, error) {
	sum := input + output + cacheRead + cacheCreate
	switch {
	case totalProvided && breakdownProvided:
		if total != sum {
			return state.TokenCounts{}, fmt.Errorf("--total-tokens=%d ≠ input+output+cache-read+cache-create=%d (provide one or the other, or make them agree)",
				total, sum)
		}
	case totalProvided && !breakdownProvided:
		input = total
	}
	return state.TokenCounts{Input: input, Output: output, CacheRead: cacheRead, CacheCreate: cacheCreate}, nil
}

// agentRoleForStage returns the canonical L3 agent name for a stage.
func agentRoleForStage(s state.Stage) string {
	switch s {
	case state.StageHydrate:
		return "story-hydrator"
	case state.StageATDD:
		return "atdd-writer"
	case state.StageImplement:
		return "tdd-implementer"
	case state.StageAutomate:
		return "test-automate"
	case state.StageTestReview:
		return "test-reviewer"
	case state.StageCodeReview:
		return "code-reviewer"
	case state.StageCommit:
		return "smart-committer"
	default:
		return string(s)
	}
}
