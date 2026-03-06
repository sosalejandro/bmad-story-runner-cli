package cmd

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sosalejandro/bmad-story-runner-cli/application"
	"github.com/sosalejandro/bmad-story-runner-cli/domain"
	"github.com/sosalejandro/bmad-story-runner-cli/infrastructure"
)

func newStatusCmd() *cobra.Command {
	var group int
	var filterGroup bool

	cmd := &cobra.Command{
		Use:   "status <progress-json>",
		Short: "Show progress summary",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			store := infrastructure.NewJSONProgressStore(log)
			uc := application.NewGetStatusUseCase(store, log)

			summary, err := uc.Execute(args[0])
			exitOnError(err)

			p := summary.Progress
			fmt.Printf("\nBMAD Progress -- %s\n", p.DocsFolder)
			fmt.Println(strings.Repeat("-", 50))

			if len(summary.Groups) > 0 {
				keys := make([]int, 0, len(summary.Groups))
				for k := range summary.Groups {
					keys = append(keys, k)
				}
				sort.Ints(keys)
				for _, g := range keys {
					if filterGroup && g != group {
						continue
					}
					stories := summary.Groups[g]
					done := 0
					for _, s := range stories {
						if s.Status == domain.StatusComplete {
							done++
						}
					}
					fmt.Printf("Group %d: %d / %d complete\n", g, done, len(stories))
				}
			} else {
				total := len(p.Stories)
				done := summary.Counts[domain.StatusComplete]
				fmt.Printf("Stories: %d / %d complete\n", done, total)
			}

			fmt.Println()
			statusOrder := []domain.Status{
				domain.StatusInProgress, domain.StatusQAReview,
				domain.StatusBlocked, domain.StatusPending,
			}
			for _, st := range statusOrder {
				if n := summary.Counts[st]; n > 0 {
					fmt.Printf("  %-12s %d\n", string(st)+":", n)
				}
			}

			var blocked []*domain.Story
			for _, s := range p.Stories {
				if s.Status == domain.StatusBlocked {
					blocked = append(blocked, s)
				}
			}
			if len(blocked) > 0 {
				fmt.Println()
				for _, s := range blocked {
					fmt.Printf("  Blocked: %s (waiting on: %v)\n", s.ID, s.Blockers)
				}
			}

			eligible := p.EligibleStories(nil)
			if len(eligible) > 0 {
				fmt.Printf("\nUp next: %s -- %s\n", eligible[0].ID, eligible[0].Title)
			} else if summary.Counts[domain.StatusComplete] == len(p.Stories) {
				fmt.Println("\nAll stories complete.")
			}
			fmt.Println()
		},
	}

	cmd.Flags().IntVar(&group, "group", 0, "Filter to a specific parallel group number")
	cmd.Flags().BoolVar(&filterGroup, "filter-group", false, "Enable group filtering (use with --group)")

	return cmd
}
