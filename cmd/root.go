package cmd

import (
	"os"

	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var log *zap.Logger

func NewRootCmd(logger *zap.Logger) *cobra.Command {
	log = logger

	root := &cobra.Command{
		Use:   "bmad",
		Short: "BMAD Story Runner CLI — manage bmad-progress.json without ad-hoc scripts",
		Long: `bmad is a CLI tool for the BMAD Story Runner skill.
It manages progress tracking, story status, QA gates, and reconciliation
so Claude does not need to write inline Python or bash scripts.`,
	}

	root.AddCommand(
		newInitCmd(),
		newStatusCmd(),
		newSetStatusCmd(),
		newSetCompleteCmd(),
		newBulkCompleteCmd(),
		newAddConcernsCmd(),
		newNextCmd(),
		newMarkStoryFileCmd(),
		newScanCmd(),
		newAssignSessionCmd(),
		newQAPendingCmd(),
		newGateCheckCmd(),
		newReconcileCmd(),
	)

	return root
}

func exitOnError(err error) {
	if err != nil {
		log.Error(err.Error())
		os.Exit(1)
	}
}
