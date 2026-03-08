package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sosalejandro/bmad-story-runner-cli/application"
	"github.com/sosalejandro/bmad-story-runner-cli/infrastructure"
)

func newLogStatsCmd() *cobra.Command {
	var global bool
	var since string
	var until string

	cmd := &cobra.Command{
		Use:   "stats",
		Short: "Show aggregated performance report",
		Run: func(cmd *cobra.Command, args []string) {
			logPath := resolveLogPath(global, "")
			lw := infrastructure.NewJSONLLogWriter(logPath, globalLogPath())

			entries, err := lw.ReadEntries(logPath)
			exitOnError(err)

			if len(entries) == 0 {
				fmt.Println("No log entries found.")
				return
			}

			report := application.ComputeStats(entries)

			fmt.Println()
			fmt.Println("Command Performance Summary")
			fmt.Println(strings.Repeat("-", 90))
			fmt.Printf("%-16s %5s %8s %8s %8s %8s %12s %10s %6s\n",
				"Command", "Count", "Avg(ms)", "P50(ms)", "P95(ms)", "P99(ms)", "Avg Heap(KB)", "Peak RSS", "Errors")
			for _, cs := range report.Commands {
				fmt.Printf("%-16s %5d %8.1f %8.1f %8.1f %8.1f %12.0f %8.0fMB %6d\n",
					cs.Name, cs.Count, cs.AvgMs, cs.P50Ms, cs.P95Ms, cs.P99Ms,
					cs.AvgHeapKB, cs.MaxPeakRSSMB, cs.Errors)
			}

			fmt.Printf("\nTotal commands: %d\n", report.TotalCommands)
			fmt.Printf("Total exec calls: %d\n", report.TotalExecs)
			fmt.Printf("Total errors: %d\n", report.TotalErrors)
			fmt.Printf("Sessions: %d unique\n", report.Sessions)

			if len(report.ExecReasons) > 0 {
				fmt.Println("\nTop exec reasons:")
				for _, rs := range report.ExecReasons {
					fmt.Printf("  %-30s %3d calls, avg %6.0fms, avg RSS %4.0fMB\n",
						`"`+rs.Reason+`"`, rs.Count, rs.AvgMs, rs.AvgRSSMB)
				}
			}
			fmt.Println()
		},
	}

	cmd.Flags().BoolVar(&global, "global", false, "Aggregate from global log")
	cmd.Flags().StringVar(&since, "since", "", "Start of reporting period (YYYY-MM-DD)")
	cmd.Flags().StringVar(&until, "until", "", "End of reporting period (YYYY-MM-DD)")

	return cmd
}
