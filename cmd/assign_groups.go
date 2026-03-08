package cmd

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sosalejandro/bmad-story-runner-cli/application"
	"github.com/sosalejandro/bmad-story-runner-cli/infrastructure"
)

func newAssignGroupsCmd() *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:   "assign-groups <progress-json> <n>",
		Short: "Distribute stories across N parallel groups by epic/module",
		Long: `Reads the progress file and assigns a parallel_group (1..N) to every story
that does not already have one.

Stories that share the same top-level subdirectory (e.g. "epic-1/") or the
same first numeric ID segment (e.g. "1" in "1.2.some-story") are always kept
in the same group to minimise merge conflicts between parallel agents.

Groups are balanced by story count (greedy bin-packing), so each agent
receives roughly the same amount of work.

Use --force to overwrite existing group assignments (e.g. when re-balancing).`,
		Args: cobra.ExactArgs(2),
		Run: func(cmd *cobra.Command, args []string) {
			progressPath := args[0]
			n, err := strconv.Atoi(args[1])
			if err != nil || n < 1 {
				exitOnError(fmt.Errorf("N must be a positive integer, got %q", args[1]))
			}

			store := infrastructure.NewJSONProgressStore(log)
			uc := application.NewAssignGroupsUseCase(store, log)

			result, err := uc.Execute(progressPath, n, force)
			exitOnError(err)

			fmt.Printf("Assigned %d stories across %d groups:\n\n", result.Total, n)
			for _, g := range result.Groups {
				modules := strings.Join(g.Modules, ", ")
				if modules == "" {
					modules = "(none)"
				}
				fmt.Printf("  Group %d: %d stories  [%s]\n", g.Group, g.Count, modules)
			}
			if result.AlreadySet > 0 && force {
				fmt.Printf("\n(overwrote %d existing group assignments)\n", result.AlreadySet)
			}
		},
	}

	cmd.Flags().BoolVar(&force, "force", false, "Overwrite existing group assignments")
	return cmd
}
