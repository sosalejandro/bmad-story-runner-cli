package cmd

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/sosalejandro/bmad-story-runner-cli/application"
	"github.com/sosalejandro/bmad-story-runner-cli/infrastructure"
)

func newInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init <docs-folder>",
		Short: "Scan a docs folder and create bmad-progress.json",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			docsFolder := args[0]

			progressPath := filepath.Join(docsFolder, "bmad-progress.json")
			store := infrastructure.NewJSONProgressStore(log)
			scanner := infrastructure.NewFSStoryScanner(log)
			uc := application.NewInitProgressUseCase(scanner, store, log)

			result, err := uc.Execute(docsFolder, progressPath)
			exitOnError(err)

			fmt.Printf("Created %s with %d stories.\n", progressPath, result.StoriesFound)
			if len(result.FlaggedAsComplete) > 0 {
				fmt.Printf("WARNING: %d stories appear complete but ci_passed=false (unverified): %v\n",
					len(result.FlaggedAsComplete), result.FlaggedAsComplete)
			}
		},
	}
}
