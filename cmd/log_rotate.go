package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sosalejandro/bmad-story-runner-cli/infrastructure"
)

func newLogRotateCmd() *cobra.Command {
	var global bool
	var projectPath string

	cmd := &cobra.Command{
		Use:   "rotate",
		Short: "Archive current log and start fresh",
		Run: func(cmd *cobra.Command, args []string) {
			logPath := resolveLogPath(global, projectPath)
			lw := infrastructure.NewJSONLLogWriter(logPath, globalLogPath())

			if err := lw.Rotate(logPath); err != nil {
				exitOnError(err)
			}
			fmt.Printf("Rotated %s\n", logPath)
		},
	}

	cmd.Flags().BoolVar(&global, "global", false, "Rotate global log")
	cmd.Flags().StringVar(&projectPath, "project", "", "Path to project log")

	return cmd
}
