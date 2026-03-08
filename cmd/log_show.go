package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/sosalejandro/bmad-story-runner-cli/domain"
	"github.com/sosalejandro/bmad-story-runner-cli/infrastructure"
)

func newLogShowCmd() *cobra.Command {
	var last int
	var entryType string
	var global bool
	var projectPath string
	var since string
	var until string
	var jsonOutput bool

	cmd := &cobra.Command{
		Use:   "show",
		Short: "View recent audit log entries",
		Run: func(cmd *cobra.Command, args []string) {
			logPath := resolveLogPath(global, projectPath)
			lw := infrastructure.NewJSONLLogWriter(logPath, globalLogPath())

			entries, err := lw.ReadEntries(logPath)
			exitOnError(err)

			if len(entries) == 0 {
				fmt.Println("No log entries found.")
				return
			}

			// Filter by type.
			if entryType != "" {
				var filtered []*domain.LogEntry
				for _, e := range entries {
					if e.Type == entryType {
						filtered = append(filtered, e)
					}
				}
				entries = filtered
			}

			// Filter by date range.
			if since != "" {
				sinceTime, err := time.Parse("2006-01-02", since)
				exitOnError(err)
				var filtered []*domain.LogEntry
				for _, e := range entries {
					if !e.Timestamp.Before(sinceTime) {
						filtered = append(filtered, e)
					}
				}
				entries = filtered
			}
			if until != "" {
				untilTime, err := time.Parse("2006-01-02", until)
				exitOnError(err)
				// Include the entire "until" day.
				untilTime = untilTime.Add(24 * time.Hour)
				var filtered []*domain.LogEntry
				for _, e := range entries {
					if e.Timestamp.Before(untilTime) {
						filtered = append(filtered, e)
					}
				}
				entries = filtered
			}

			// Take last N entries.
			if last > 0 && len(entries) > last {
				entries = entries[len(entries)-last:]
			}

			// Render.
			for _, e := range entries {
				if jsonOutput {
					data, err := json.Marshal(e)
					if err == nil {
						fmt.Println(string(data))
					}
					continue
				}
				printEntry(e)
			}
		},
	}

	cmd.Flags().IntVar(&last, "last", 20, "Show the N most recent entries")
	cmd.Flags().StringVar(&entryType, "type", "", "Filter by entry type: command, exec, session_start")
	cmd.Flags().BoolVar(&global, "global", false, "Read from global log")
	cmd.Flags().StringVar(&projectPath, "project", "", "Path to project log")
	cmd.Flags().StringVar(&since, "since", "", "Show entries after date (YYYY-MM-DD)")
	cmd.Flags().StringVar(&until, "until", "", "Show entries before date (YYYY-MM-DD)")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output raw JSONL")

	return cmd
}

func globalLogPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".bmad", "audit.jsonl")
}

func resolveLogPath(global bool, projectPath string) string {
	if global {
		return globalLogPath()
	}
	if projectPath != "" {
		return projectPath
	}
	// Try to find project log in current directory tree.
	// Look for bmad-audit.jsonl in cwd or parent directories.
	cwd, _ := os.Getwd()
	for dir := cwd; dir != "/"; dir = filepath.Dir(dir) {
		candidate := filepath.Join(dir, "bmad-audit.jsonl")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return globalLogPath()
}

func printEntry(e *domain.LogEntry) {
	ts := e.Timestamp.Format("2006-01-02 15:04:05")
	switch e.Type {
	case domain.EntryTypeSessionStart:
		fmt.Printf("[%s] SESSION START\n", ts)
		fmt.Printf("  pwd:     %s\n", e.Session.PWD)
		fmt.Printf("  pid:     %d\n", e.Session.PID)
		fmt.Printf("  bmad:    %s (%s)\n", e.Session.BmadVersion, e.Session.BmadCommit)
		fmt.Println()

	case domain.EntryTypeCommand:
		fmt.Printf("[%s] COMMAND %s\n", ts, e.Command.Name)
		if len(e.Command.Args) > 0 {
			fmt.Printf("  args:  %s\n", strings.Join(e.Command.Args, " "))
		}
		if e.Result != nil {
			fmt.Printf("  exit:  %d\n", e.Result.ExitCode)
		}
		if e.Performance != nil {
			fmt.Printf("  time:  %.1fms\n", e.Performance.TotalMs)
		}
		if e.Memory != nil {
			fmt.Printf("  mem:   heap=%dKB peak_rss=%dMB gc=%d\n",
				e.Memory.HeapAllocBytes/1024,
				e.Memory.PeakRSSBytes/(1024*1024),
				e.Memory.NumGC)
		}
		fmt.Println()

	case domain.EntryTypeExec:
		childCmd := ""
		if e.Exec != nil {
			childCmd = e.Exec.ChildCommand + " " + strings.Join(e.Exec.ChildArgs, " ")
		}
		fmt.Printf("[%s] EXEC %s\n", ts, childCmd)
		if e.Exec != nil {
			if e.Exec.Context.Reason != "" {
				fmt.Printf("  reason:   %s\n", e.Exec.Context.Reason)
			}
			if e.Exec.Context.PreviousBmadCommand != "" {
				fmt.Printf("  context:  after '%s'\n", e.Exec.Context.PreviousBmadCommand)
			}
			fmt.Printf("  child:    %.0fms (user: %.0fms, sys: %.0fms, rss: %dMB)\n",
				e.Exec.ChildDurationMs,
				e.Exec.ChildUserTimeMs,
				e.Exec.ChildSysTimeMs,
				e.Exec.ChildPeakRSSBytes/(1024*1024))
		}
		fmt.Println()
	}
}
