package cmd

import "github.com/spf13/cobra"

func newLogCmd() *cobra.Command {
	return &cobra.Command{Use: "log", Short: "View and manage audit logs"}
}
