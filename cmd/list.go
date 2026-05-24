package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sosalejandro/bmad-story-runner-cli/application"
	appstate "github.com/sosalejandro/bmad-story-runner-cli/application/state"
	"github.com/sosalejandro/bmad-story-runner-cli/domain"
	"github.com/sosalejandro/bmad-story-runner-cli/domain/state"
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
		Use:   "list [<progress-json>]",
		Short: "List stories with filtering by group, status, and blocker resolution",
		Long: `List stories with flexible filtering.

Backend auto-detection (issue #71):
  - no positional arg, or arg ending in .db → v6 SQLite store
    (path resolved via --state, $BMAD_STATE, or ./bmad-state.db)
  - arg ending in .json → legacy v4 bmad-progress.json
  - other: peek the file header for the SQLite magic bytes; fall back
    to the v4 JSON store if not a SQLite database

Examples:
  bmad list --status pending --unblocked-only      # SQLite (default)
  bmad list ./other-sprint.db --status pending     # SQLite (explicit)
  bmad list bmad-progress.json --group 3 --filter-group  # legacy JSON`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			posArg := ""
			if len(args) == 1 {
				posArg = args[0]
			}
			backend, _ := resolveStateBackend(posArg)

			if backend == backendSQLite {
				return runListSQLite(cmd, status, group, filterGroup, unblockedOnly, showBlockers)
			}
			return runListJSON(posArg, status, group, filterGroup, unblockedOnly, showBlockers)
		},
	}

	cmd.Flags().IntVar(&group, "group", 0, "Filter to a specific parallel group number")
	cmd.Flags().BoolVar(&filterGroup, "filter-group", false, "Enable group filtering (use with --group)")
	cmd.Flags().StringVar(&status, "status", "", "Filter by status (pending, in-progress, qa-review, complete, blocked)")
	cmd.Flags().BoolVar(&showBlockers, "show-blockers", false, "Show blocker IDs with resolution status")
	cmd.Flags().BoolVar(&unblockedOnly, "unblocked-only", false, "Only show stories whose blockers are all resolved")
	// --state shares the persistent flag block with the rest of v6 cmds so
	// `bmad list --state /tmp/sprint.db` works without a positional arg.
	addV6PersistentFlags(cmd)

	return cmd
}

// runListSQLite is the v6 path — opens the resolved bmad-state.db and runs
// the application.state.StoryService.List use case.
func runListSQLite(cmd *cobra.Command, status string, group int, filterGroup, unblockedOnly, showBlockers bool) error {
	ctx := context.Background()
	svc, cleanup, err := openStoryService(ctx)
	if err != nil {
		return err
	}
	defer cleanup()

	filter := appstate.ListFilter{UnblockedOnly: unblockedOnly}
	if filterGroup {
		g := group
		filter.ParallelGroup = &g
	}
	if status != "" {
		st := state.Status(status)
		filter.Status = &st
	}

	rows, err := svc.List(ctx, filter)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		fmt.Println("No stories match the given filters.")
		return nil
	}
	printListTableV6(rows, showBlockers)
	return nil
}

// runListJSON is the legacy v4 path — preserved for back-compat with
// pre-SQLite-migration workflows that still drive a bmad-progress.json.
func runListJSON(path, status string, group int, filterGroup, unblockedOnly, showBlockers bool) error {
	store := infrastructure.NewJSONProgressStore(log)
	uc := application.NewListStoriesUseCase(store, log)

	filter := application.ListFilter{UnblockedOnly: unblockedOnly}
	if filterGroup {
		g := group
		filter.Group = &g
	}
	if status != "" {
		st, err := domain.ParseStatus(status)
		if err != nil {
			return err
		}
		filter.Status = &st
	}

	rows, err := uc.Execute(path, filter)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		fmt.Println("No stories match the given filters.")
		return nil
	}
	printListTable(rows, showBlockers)
	return nil
}

func printListTableV6(rows []appstate.ListRow, showBlockers bool) {
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
			fmt.Printf("%-*s | %-*s | %s\n", maxStatus, string(r.Story.Status), maxID, r.Story.ID, formatBlockersV6(r.Blockers))
		} else {
			fmt.Printf("%-*s | %-*s | %s\n", maxStatus, string(r.Story.Status), maxID, r.Story.ID, r.Story.Title)
		}
	}

	fmt.Printf("\n%d stories listed.\n", len(rows))
}

func formatBlockersV6(blockers []appstate.ListBlocker) string {
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
