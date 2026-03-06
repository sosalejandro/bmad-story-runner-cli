package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sosalejandro/bmad-story-runner-cli/application"
	"github.com/sosalejandro/bmad-story-runner-cli/infrastructure"
)

func newSetStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set-status <progress-json> <story-id> <status>",
		Short: "Update a story's status",
		Long:  "Valid statuses: pending, in-progress, qa-review, complete, blocked",
		Args:  cobra.ExactArgs(3),
		Run: func(cmd *cobra.Command, args []string) {
			store := infrastructure.NewJSONProgressStore(log)
			uc := application.NewSetStatusUseCase(store, log)
			exitOnError(uc.Execute(args[0], args[1], args[2]))
			fmt.Printf("Set %s status -> %s\n", args[1], args[2])
		},
	}
}
