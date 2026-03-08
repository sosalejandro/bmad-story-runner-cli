package cmd

import "github.com/spf13/cobra"

func newLogCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "log",
		Short: "View and manage audit logs",
		Long:  "Query, analyze, and rotate bmad audit logs.",
	}

	cmd.AddCommand(
		newLogShowCmd(),
		newLogStatsCmd(),
		newLogPatternsCmd(),
		newLogRotateCmd(),
	)

	return cmd
}
