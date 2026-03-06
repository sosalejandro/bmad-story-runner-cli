package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sosalejandro/bmad-story-runner-cli/application"
	"github.com/sosalejandro/bmad-story-runner-cli/infrastructure"
)

func newReconcileCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "reconcile <progress-json>",
		Short: "Read gate files and update progress JSON (PASS->complete, FAIL/CONCERNS->keep qa-review)",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			store := infrastructure.NewJSONProgressStore(log)
			gateReader := infrastructure.NewFSGateReader(log)
			uc := application.NewReconcileUseCase(gateReader, store, log)

			result, err := uc.Execute(args[0])
			exitOnError(err)

			if len(result.Completed) > 0 {
				fmt.Printf("Completed (%d): %v\n", len(result.Completed), result.Completed)
			}
			if len(result.Blocked) > 0 {
				fmt.Printf("Still blocked (%d): %v\n", len(result.Blocked), result.Blocked)
			}
			if len(result.Completed) == 0 && len(result.Blocked) == 0 {
				fmt.Println("No qa-review stories with gate files found.")
			}
		},
	}
}
