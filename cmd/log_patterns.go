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

func newLogPatternsCmd() *cobra.Command {
	var global bool
	var minCount int
	var sortBy string

	cmd := &cobra.Command{
		Use:   "patterns",
		Short: "Identify recurring exec workaround patterns",
		Run: func(cmd *cobra.Command, args []string) {
			logPath := resolveLogPath(global, "")
			lw := infrastructure.NewJSONLLogWriter(logPath, globalLogPath())

			entries, err := lw.ReadEntries(logPath)
			exitOnError(err)

			report := application.ComputeStats(entries)

			// Filter by minimum count.
			var reasons []application.ExecReasonStats
			for _, rs := range report.ExecReasons {
				if rs.Count >= minCount {
					reasons = append(reasons, rs)
				}
			}

			if len(reasons) == 0 {
				fmt.Println("No exec patterns found (try --min-count 1).")
				return
			}

			// Sort.
			switch sortBy {
			case "duration":
				sort.Slice(reasons, func(i, j int) bool { return reasons[i].AvgMs > reasons[j].AvgMs })
			case "memory":
				sort.Slice(reasons, func(i, j int) bool { return reasons[i].AvgRSSMB > reasons[j].AvgRSSMB })
			default: // count
				sort.Slice(reasons, func(i, j int) bool { return reasons[i].Count > reasons[j].Count })
			}

			fmt.Printf("\nExec Pattern Analysis (%d total exec calls)\n", report.TotalExecs)
			fmt.Println(strings.Repeat("-", 80))
			fmt.Printf("%-30s %5s %8s %10s %10s\n", "Reason", "Count", "Avg(ms)", "Avg RSS", "Total Time")
			for _, rs := range reasons {
				fmt.Printf("%-30s %5d %8.0fms %8.0fMB %8.1fs\n",
					`"`+rs.Reason+`"`, rs.Count, rs.AvgMs, rs.AvgRSSMB, rs.TotalTimeS)
			}

			// Child command breakdown.
			childCmds := make(map[string]int)
			for _, e := range entries {
				if e.Type == domain.EntryTypeExec && e.Exec != nil {
					childCmds[e.Exec.ChildCommand]++
				}
			}
			if len(childCmds) > 0 {
				fmt.Println("\nTop child commands:")
				type kv struct {
					k string
					v int
				}
				var sorted []kv
				for k, v := range childCmds {
					sorted = append(sorted, kv{k, v})
				}
				sort.Slice(sorted, func(i, j int) bool { return sorted[i].v > sorted[j].v })
				for _, s := range sorted {
					pct := float64(s.v) / float64(report.TotalExecs) * 100
					fmt.Printf("  %-12s %d calls (%.0f%%)\n", s.k, s.v, pct)
				}
			}

			fmt.Println()
		},
	}

	cmd.Flags().BoolVar(&global, "global", false, "Analyze global log")
	cmd.Flags().IntVar(&minCount, "min-count", 2, "Minimum occurrences to show")
	cmd.Flags().StringVar(&sortBy, "sort", "count", "Sort by: count, duration, memory")

	return cmd
}
