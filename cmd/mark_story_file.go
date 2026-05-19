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
			if jsonOutput {
				_ = emitJSONStdout(commandPathSansRoot(cmd),
					map[string]any{"file_path": args[0], "status": args[1]},
					map[string]any{
						"ok":        true,
						"file_path": args[0],
						"status":    args[1],
					}, nil)
				return
			}
			fmt.Printf("Updated status in %s -> %s\n", args[0], args[1])
		},
	}
}
