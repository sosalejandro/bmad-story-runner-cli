package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/sosalejandro/bmad-story-runner-cli/domain/state"
	"github.com/sosalejandro/bmad-story-runner-cli/infrastructure/state/sqlite"
)

// newDispatchCmd is the `bmad dispatch <verb>` namespace. Single verb today
// (record); leaves room for future verbs like `dispatch replay <id>`.
func newDispatchCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dispatch <verb>",
		Short: "Record L3 agent dispatch results (token cost + status per §12.7)",
	}
	cmd.AddCommand(newDispatchRecordCmd())
	addV6PersistentFlags(cmd)
	return cmd
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
	)
	cmd := &cobra.Command{
		Use:   "record <story-id> <stage> <status>",
		Short: "Insert a dispatches row capturing one L3 invocation's return",
		Long: `status must be ok | blocked | errored. Token counts default to 0
when omitted (useful for stub records). --agent defaults to a
stage-derived role name.`,
		Args: cobra.ExactArgs(3),
		RunE: func(c *cobra.Command, args []string) error {
			ctx := context.Background()
			db, err := openV6DB(ctx)
			if err != nil {
				return err
			}
			defer db.Close()

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

			now := time.Now().UTC()
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

			id, err := sqlite.NewDispatchesStore(db).Insert(ctx, d)
			if err != nil {
				return err
			}
			fmt.Printf("dispatch %d recorded: %s/%s/%s (tokens %d:%d:%d:%d, %dms)\n",
				id, storyID, stage, status,
				inputTokens, outputTokens, cacheRead, cacheCreate, durationMS)
			return nil
		},
	}
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
