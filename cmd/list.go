package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sosalejandro/bmad-story-runner-cli/application"
	"github.com/sosalejandro/bmad-story-runner-cli/domain"
	"github.com/sosalejandro/bmad-story-runner-cli/infrastructure"
)

func newListCmd() *cobra.Command {
	var (
		group         int
		filterGroup   bool
		status        string
		showBlockers  bool
		unblockedOnly bool
	)

	cmd := &cobra.Command{
		Use:   "list <progress-json>",
		Short: "List stories with filtering by group, status, and blocker resolution",
		Long: `List stories from the progress file with flexible filtering.

Examples:
  bmad list progress.json --group 3 --filter-group
  bmad list progress.json --status pending --show-blockers
  bmad list progress.json --group 3 --filter-group --unblocked-only`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store := infrastructure.NewJSONProgressStore(log)
			uc := application.NewListStoriesUseCase(store, log)

			filter := application.ListFilter{
				UnblockedOnly: unblockedOnly,
			}
			if filterGroup {
				filter.Group = &group
			}
			if status != "" {
				st, err := domain.ParseStatus(status)
				if err != nil {
					return err
				}
				filter.Status = &st
			}

			rows, err := uc.Execute(args[0], filter)
			if err != nil {
				return err
			}

			if len(rows) == 0 {
				fmt.Println("No stories match the given filters.")
				return nil
			}

			printListTable(rows, showBlockers)
			return nil
		},
	}

	cmd.Flags().IntVar(&group, "group", 0, "Filter to a specific parallel group number")
	cmd.Flags().BoolVar(&filterGroup, "filter-group", false, "Enable group filtering (use with --group)")
	cmd.Flags().StringVar(&status, "status", "", "Filter by status (pending, in-progress, qa-review, complete, blocked)")
	cmd.Flags().BoolVar(&showBlockers, "show-blockers", false, "Show blocker IDs with resolution status")
	cmd.Flags().BoolVar(&unblockedOnly, "unblocked-only", false, "Only show stories whose blockers are all resolved")

	return cmd
}

func printListTable(rows []application.ListRow, showBlockers bool) {
	// Calculate column widths.
	maxStatus := len("STATUS")
	maxID := len("STORY ID")
	for _, r := range rows {
		if l := len(string(r.Story.Status)); l > maxStatus {
			maxStatus = l
		}
		if l := len(r.Story.ID); l > maxID {
			maxID = l
		}
	}

	if showBlockers {
		fmt.Printf("%-*s | %-*s | BLOCKERS\n", maxStatus, "STATUS", maxID, "STORY ID")
		fmt.Printf("%s-|-%s-|-%s\n", repeat("-", maxStatus), repeat("-", maxID), repeat("-", 30))
	} else {
		fmt.Printf("%-*s | %-*s | TITLE\n", maxStatus, "STATUS", maxID, "STORY ID")
		fmt.Printf("%s-|-%s-|-%s\n", repeat("-", maxStatus), repeat("-", maxID), repeat("-", 30))
	}

	for _, r := range rows {
		if showBlockers {
			blockerStr := formatBlockers(r.Blockers)
			fmt.Printf("%-*s | %-*s | %s\n", maxStatus, string(r.Story.Status), maxID, r.Story.ID, blockerStr)
		} else {
			fmt.Printf("%-*s | %-*s | %s\n", maxStatus, string(r.Story.Status), maxID, r.Story.ID, r.Story.Title)
		}
	}

	fmt.Printf("\n%d stories listed.\n", len(rows))
}

func formatBlockers(blockers []application.BlockerInfo) string {
	if len(blockers) == 0 {
		return "-"
	}
	parts := make([]string, 0, len(blockers))
	for _, b := range blockers {
		marker := "✗"
		if b.Resolved {
			marker = "✓"
		}
		parts = append(parts, fmt.Sprintf("%s (%s)", b.ID, marker))
	}
	return strings.Join(parts, ", ")
}
