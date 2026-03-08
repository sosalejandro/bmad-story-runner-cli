package cmd

import "github.com/spf13/cobra"

func newExecCmd() *cobra.Command {
	return &cobra.Command{Use: "exec", Short: "Run a command with audit logging"}
}
