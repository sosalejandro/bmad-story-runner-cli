package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sosalejandro/bmad-story-runner-cli/infrastructure"
)

func newMarkStoryFileCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "mark-story-file <story-file> <status>",
		Short: "Update the **Status:** line in a story .md file",
		Args:  cobra.ExactArgs(2),
		Run: func(cmd *cobra.Command, args []string) {
			patcher := infrastructure.NewMDStoryFilePatcher(log)
			exitOnError(patcher.PatchStatus(args[0], args[1]))
			fmt.Printf("Updated status in %s -> %s\n", args[0], args[1])
		},
	}
}
