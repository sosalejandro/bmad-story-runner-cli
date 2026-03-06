package cmd

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/sosalejandro/bmad-story-runner-cli/application"
	"github.com/sosalejandro/bmad-story-runner-cli/infrastructure"
)

func newAssignSessionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "assign-session <progress-json> <group> <session-id>",
		Short: "Assign a session ID to all unassigned stories in a parallel group",
		Args:  cobra.ExactArgs(3),
		Run: func(cmd *cobra.Command, args []string) {
			group, err := strconv.Atoi(args[1])
			if err != nil {
				exitOnError(fmt.Errorf("parsing group number: %w", err))
			}

			store := infrastructure.NewJSONProgressStore(log)
			uc := application.NewAssignSessionUseCase(store, log)

			count, err := uc.Execute(args[0], group, args[2])
			exitOnError(err)
			fmt.Printf("Assigned session %q to %d stories in group %d\n", args[2], count, group)
		},
	}
}
