package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sosalejandro/bmad-story-runner-cli/application"
	"github.com/sosalejandro/bmad-story-runner-cli/infrastructure"
)

func newScanCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "scan <docs-folder>",
		Short: "List stories with task/AC completion counts",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			reporter := infrastructure.NewFSStoryScanReporter(log)
			uc := application.NewScanStoriesUseCase(reporter, log)

			results, err := uc.Execute(args[0])
			exitOnError(err)

			fmt.Printf("%-12s %-6s %-12s %s\n", "STORY", "ACs", "TASKS", "TITLE")
			fmt.Println(repeat("-", 60))
			for _, r := range results {
				fmt.Printf("%-12s %-6d %d/%-9d %s\n",
					r.StoryID, r.ACCount, r.TasksDone, r.TasksTotal, r.Title)
			}
		},
	}
}
