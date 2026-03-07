package cmd

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sosalejandro/bmad-story-runner-cli/application"
	"github.com/sosalejandro/bmad-story-runner-cli/domain"
	"github.com/sosalejandro/bmad-story-runner-cli/infrastructure"
)

func newBulkCompleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "bulk-complete <progress-json> <story-id> [story-id ...]",
		Short: "Mark multiple stories complete in a single write (after CI passes)",
		Long: `Atomically marks all listed stories complete (status=complete, ci_passed=true,
completed_at=now) in a single JSON write. Use when CI has passed for a batch
of stories — e.g. after parallel QA agents have reviewed and approved them.

Example:
  bmad bulk-complete ./bmad-progress.json \
    3.2.webhook-handler \
    4.4.stripe-integration \
    5.7.refund-flow`,
		Args: cobra.MinimumNArgs(2),
		Run: func(cmd *cobra.Command, args []string) {
			progressPath := args[0]
			storyIDs := args[1:]

			store := infrastructure.NewJSONProgressStore(log)
			uc := application.NewBulkCompleteUseCase(store, log)

			result, err := uc.Execute(progressPath, storyIDs)

			// Report completed stories even if some were not found.
			if len(result.Completed) > 0 {
				fmt.Printf("Completed (%d):\n", len(result.Completed))
				for _, id := range result.Completed {
					fmt.Printf("  ✓ %s\n", id)
				}
			}

			if err != nil {
				if len(result.NotFound) > 0 {
					fmt.Printf("\nNot found (%d):\n", len(result.NotFound))
					for _, id := range result.NotFound {
						fmt.Printf("  ✗ %s\n", id)
					}
				}
				// Only treat missing stories as a hard error if nothing was completed.
				if len(result.Completed) == 0 {
					exitOnError(err)
				} else {
					// Partial success: warn but don't exit non-zero so callers can continue.
					fmt.Printf("\nWARNING: %d story ID(s) not found in progress file\n", len(result.NotFound))
					_ = errors.Is(err, domain.ErrStoryNotFound) // guard: surface sentinel type
				}
				return
			}

			fmt.Printf("\nAll %d stories marked complete.\n", len(result.Completed))
		},
	}
}
