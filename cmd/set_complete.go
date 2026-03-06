package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sosalejandro/bmad-story-runner-cli/application"
	"github.com/sosalejandro/bmad-story-runner-cli/infrastructure"
)

func newSetCompleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set-complete <progress-json> <story-id>",
		Short: "Mark a story complete after CI passes (sets ci_passed=true atomically)",
		Args:  cobra.ExactArgs(2),
		Run: func(cmd *cobra.Command, args []string) {
			store := infrastructure.NewJSONProgressStore(log)
			uc := application.NewSetCompleteUseCase(store, log)
			exitOnError(uc.Execute(args[0], args[1]))
			fmt.Printf("Marked %s complete (ci_passed=true)\n", args[1])
		},
	}
}
