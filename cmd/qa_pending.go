package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sosalejandro/bmad-story-runner-cli/application"
	"github.com/sosalejandro/bmad-story-runner-cli/infrastructure"
)

func newQAPendingCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "qa-pending <docs-folder>",
		Short: "List story files that still contain placeholder QA sections",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			scanner := infrastructure.NewFSQAPendingScanner(log)
			uc := application.NewQAPendingUseCase(scanner, log)

			files, err := uc.Execute(args[0])
			exitOnError(err)

			if len(files) == 0 {
				fmt.Println("No stories with pending QA sections.")
				return
			}

			fmt.Printf("%d story file(s) with pending QA:\n", len(files))
			for _, f := range files {
				fmt.Printf("  %s\n", f)
			}
		},
	}
}
