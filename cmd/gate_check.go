package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/sosalejandro/bmad-story-runner-cli/application"
	"github.com/sosalejandro/bmad-story-runner-cli/infrastructure"
)

func newGateCheckCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "gate-check <progress-json>",
		Short: "Read QA gate files and print PASS/FAIL/CONCERNS table; exits non-zero if any FAIL/CONCERNS",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			store := infrastructure.NewJSONProgressStore(log)
			gateReader := infrastructure.NewFSGateReader(log)
			uc := application.NewGateCheckUseCase(gateReader, store, log)

			result, err := uc.Execute(args[0])
			exitOnError(err)

			if len(result.Gates) == 0 {
				fmt.Println("No gate files found.")
				return
			}

			fmt.Printf("%-20s %s\n", "STORY", "RESULT")
			fmt.Println(repeat("-", 35))
			for _, g := range result.Gates {
				fmt.Printf("%-20s %s\n", g.StoryID, string(g.Result))
			}

			if result.HasFails {
				fmt.Fprintln(os.Stderr, "\nGate check FAILED: one or more stories have FAIL or CONCERNS")
				os.Exit(1)
			}

			fmt.Println("\nAll gates PASS.")
		},
	}
}
