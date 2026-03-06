package cmd

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/sosalejandro/bmad-story-runner-cli/application"
	"github.com/sosalejandro/bmad-story-runner-cli/domain"
	"github.com/sosalejandro/bmad-story-runner-cli/infrastructure"
)

func newNextCmd() *cobra.Command {
	var group int
	var filterGroup bool

	cmd := &cobra.Command{
		Use:   "next <progress-json>",
		Short: "Print the file path of the next eligible story",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			store := infrastructure.NewJSONProgressStore(log)
			uc := application.NewNextStoryUseCase(store, log)

			var g *int
			if filterGroup {
				g = &group
			}

			path, err := uc.Execute(args[0], g)
			if err != nil {
				if errors.Is(err, domain.ErrNoEligibleStory) {
					fmt.Fprintln(os.Stderr, "NONE: no eligible story found")
					os.Exit(2)
				}
				exitOnError(err)
			}

			fmt.Println(path)
		},
	}

	cmd.Flags().IntVar(&group, "group", 0, "Restrict to a specific parallel group number")
	cmd.Flags().BoolVar(&filterGroup, "filter-group", false, "Enable group filtering (use with --group)")

	return cmd
}
