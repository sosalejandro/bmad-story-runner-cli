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
		agentRole    string
		attemptNo    int
		reason       string
		inputTokens  int64
		outputTokens int64
		cacheRead    int64
		cacheCreate  int64
		model        string
		durationMS   int64
		idemKey      string
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
    crash-recovery reconciliation.`,
		Args: cobra.MaximumNArgs(3),
		RunE: func(c *cobra.Command, args []string) error {
			ctx := context.Background()
			db, err := openV6DB(ctx)
			if err != nil {
				return err
			}
			defer db.Close()
			store := sqlite.NewDispatchesStore(db)
			now := time.Now().UTC()

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
	cmd.Flags().StringVar(&model, "model", "", "model identifier")
	cmd.Flags().Int64Var(&durationMS, "duration-ms", 0, "dispatch wall-clock duration in ms")
	return cmd
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
