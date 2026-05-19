package cmd

import (
	"fmt"
	"os"
	osexec "os/exec"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/sosalejandro/bmad-story-runner-cli/domain"
	"github.com/sosalejandro/bmad-story-runner-cli/infrastructure"
)

func newExecCmd() *cobra.Command {
	var reason string
	var context string

	cmd := &cobra.Command{
		Use:   "exec [--reason <why>] [--context <prev-cmd>] -- <command> [args...]",
		Short: "Run a command with audit logging and telemetry",
		Long: `Wrap an arbitrary shell command with full audit logging.
Use when the CLI doesn't have a native command for what you need.

The --reason flag describes what the command does, enabling pattern
analysis to identify operations that should become native CLI commands.

Stdin, stdout, and stderr pass through transparently.
Exit code matches the child process.`,
		DisableFlagParsing: false,
		Args:               cobra.MinimumNArgs(1),
		Run: func(c *cobra.Command, args []string) {
			if len(args) == 0 {
				fmt.Fprintln(os.Stderr, "exec requires a command after --")
				os.Exit(1)
			}

			childCmd := args[0]
			childArgs := args[1:]

			// Auto-detect context from last log entry if not provided.
			if context == "" {
				lw := getLogWriter(nil)
				if lw != nil {
					if last := lw.LastEntry(); last != nil && last.Command != nil {
						context = last.Command.Name
					}
				}
			}

			// Run the child process. Tee stdout/stderr through byte counters
			// so the audit row records what the child emitted; without this
			// child_stdout_bytes / child_stderr_bytes were always zero.
			// CountingWriter wraps the real stream and forwards every Write,
			// so output is emitted exactly once. (closes #5)
			startTime := time.Now()
			child := osexec.Command(childCmd, childArgs...) //nolint:gosec // bmad exec is a deliberate command-runner wrapper
			child.Stdin = os.Stdin
			stdoutTap := &infrastructure.CountingWriter{W: os.Stdout}
			stderrTap := &infrastructure.CountingWriter{W: os.Stderr}
			child.Stdout = stdoutTap
			child.Stderr = stderrTap

			err := child.Run()
			elapsed := time.Since(startTime)

			childExitCode := 0
			if err != nil {
				if exitErr, ok := err.(*osexec.ExitError); ok {
					childExitCode = exitErr.ExitCode()
				} else {
					fmt.Fprintf(os.Stderr, "exec error: %v\n", err)
					childExitCode = 1
				}
			}

			// Collect child process metrics.
			var userTimeMs, sysTimeMs float64
			var peakRSS uint64
			if child.ProcessState != nil {
				userTimeMs, sysTimeMs, peakRSS = infrastructure.CollectChildMetrics(child.ProcessState)
			}

			// Build and write log entry.
			if !noLog {
				session := infrastructure.CollectSessionInfo(Version, CommitSHA)
				cmdInfo := &domain.CommandInfo{
					Name: "exec",
					Args: append([]string{childCmd}, childArgs...),
					Raw:  "bmad exec --reason " + quote(reason) + " -- " + childCmd + " " + strings.Join(childArgs, " "),
				}
				execInfo := &domain.ExecInfo{
					ChildCommand:      childCmd,
					ChildArgs:         childArgs,
					ChildExitCode:     childExitCode,
					ChildDurationMs:   float64(elapsed.Milliseconds()),
					ChildStdoutBytes:  stdoutTap.Count(),
					ChildStderrBytes:  stderrTap.Count(),
					ChildPeakRSSBytes: peakRSS,
					ChildUserTimeMs:   userTimeMs,
					ChildSysTimeMs:    sysTimeMs,
					Context: domain.ExecContext{
						PreviousBmadCommand: context,
						Reason:              reason,
					},
				}

				entry := domain.NewExecEntry(session, cmdInfo, execInfo)
				memStats := infrastructure.CollectMemoryStats()
				entry.Memory = &memStats
				entry.Performance = &domain.PerformanceStats{
					TotalMs:     float64(elapsed.Milliseconds()),
					WallClockNs: elapsed.Nanoseconds(),
				}
				entry.Result = &domain.ResultInfo{
					ExitCode:    childExitCode,
					StdoutBytes: stdoutTap.Count(),
					StderrBytes: stderrTap.Count(),
				}

				lw := getLogWriter(nil)
				if lw != nil {
					lw.WriteEntry(entry) //nolint:errcheck
				}
			}

			os.Exit(childExitCode)
		},
	}

	cmd.Flags().StringVar(&reason, "reason", "", "Why this command is being run (for pattern analysis)")
	cmd.Flags().StringVar(&context, "context", "", "Which bmad command preceded this (auto-detected if omitted)")

	return cmd
}

func quote(s string) string {
	if s == "" {
		return `""`
	}
	if strings.ContainsAny(s, " \t\n") {
		return `"` + s + `"`
	}
	return s
}
