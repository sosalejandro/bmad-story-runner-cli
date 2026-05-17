package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"go.uber.org/zap"

	"github.com/sosalejandro/bmad-story-runner-cli/domain"
	"github.com/sosalejandro/bmad-story-runner-cli/infrastructure"
)

var (
	log   *zap.Logger
	noLog bool
)

func NewRootCmd(logger *zap.Logger) *cobra.Command {
	log = logger

	root := &cobra.Command{
		Use:   "bmad",
		Short: "BMAD Story Runner CLI — manage bmad-progress.json without ad-hoc scripts",
		Long: `bmad is a CLI tool for the BMAD Story Runner skill.
It manages progress tracking, story status, QA gates, and reconciliation
so Claude does not need to write inline Python or bash scripts.`,
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			// Resolve relative path args to absolute.
			if len(args) > 0 && looksLikePath(args[0]) {
				args[0] = absPath(args[0])
			}
		},
	}

	root.PersistentFlags().BoolVar(&noLog, "no-log", false, "Disable audit logging for this invocation")

	root.AddCommand(
		newInitCmd(),     // v6 — sqlite init
		newConfigCmd(),   // v6 — runtime config get/set
		newStoryCmd(),    // v6 — story namespace (status, hydrate, next, ...)
		newStatusCmd(),
		newSetStatusCmd(),
		newSetCompleteCmd(),
		newBulkCompleteCmd(),
		newAddConcernsCmd(),
		newNextCmd(),
		newMarkStoryFileCmd(),
		newScanCmd(),
		newAssignGroupsCmd(),
		newAssignSessionCmd(),
		newQAPendingCmd(),
		newGateCheckCmd(),
		newReconcileCmd(),
		newWriteGateCmd(),
		newExecCmd(),
		newListCmd(),
		newLogCmd(),
	)

	// Wrap all leaf commands with logging middleware.
	wrapCommandsWithLogging(root)

	return root
}

// wrapCommandsWithLogging wraps every leaf command's Run/RunE with audit logging.
func wrapCommandsWithLogging(parent *cobra.Command) {
	for _, cmd := range parent.Commands() {
		if cmd.HasSubCommands() {
			wrapCommandsWithLogging(cmd)
			continue
		}
		if isLogCommand(cmd) {
			continue
		}
		wrapSingleCommand(cmd)
	}
}

// isLogCommand returns true if the command or any of its parents is named "log".
func isLogCommand(cmd *cobra.Command) bool {
	for p := cmd; p != nil; p = p.Parent() {
		if p.Name() == "log" {
			return true
		}
	}
	return false
}

// wrapSingleCommand wraps a command's Run or RunE with logging middleware.
func wrapSingleCommand(cmd *cobra.Command) {
	if cmd.RunE != nil {
		original := cmd.RunE
		cmd.RunE = func(c *cobra.Command, args []string) error {
			return runWithLogging(c, args, func() error { return original(c, args) })
		}
	} else if cmd.Run != nil {
		original := cmd.Run
		cmd.Run = func(c *cobra.Command, args []string) {
			_ = runWithLogging(c, args, func() error { original(c, args); return nil })
		}
	}
}

// runWithLogging executes the command function and writes an audit log entry.
func runWithLogging(cmd *cobra.Command, args []string, fn func() error) error {
	if noLog {
		return fn()
	}

	lw := getLogWriter(args)
	if lw == nil {
		return fn()
	}

	session := infrastructure.CollectSessionInfo(Version, CommitSHA)

	// Check if this is a new session.
	if last := lw.LastEntry(); last == nil || last.IsNewSession(&domain.LogEntry{Session: session}) {
		lw.WriteEntry(domain.NewSessionStartEntry(session)) //nolint:errcheck
	}

	startTime := time.Now()

	// Run the actual command.
	err := fn()

	// Collect post-execution metrics.
	elapsed := time.Since(startTime)
	memStats := infrastructure.CollectMemoryStats()

	// Build log entry.
	cmdInfo := &domain.CommandInfo{
		Name: cmd.Name(),
		Args: args,
		Raw:  "bmad " + cmd.Name() + " " + strings.Join(args, " "),
	}

	entry := domain.NewCommandEntry(session, cmdInfo)
	entry.Performance = &domain.PerformanceStats{
		TotalMs:     float64(elapsed.Milliseconds()),
		WallClockNs: elapsed.Nanoseconds(),
	}
	entry.Memory = &memStats

	exitCode := 0
	if err != nil {
		exitCode = 1
		errMsg := err.Error()
		entry.Result = &domain.ResultInfo{
			ExitCode: exitCode,
			Error:    &errMsg,
		}
	} else {
		entry.Result = &domain.ResultInfo{ExitCode: 0}
	}

	lw.WriteEntry(entry) //nolint:errcheck
	return err
}

// getLogWriter creates a log writer based on the command args.
// It tries to detect the project log path from the progress file or docs folder argument.
func getLogWriter(args []string) *infrastructure.JSONLLogWriter {
	home, _ := os.UserHomeDir()
	if home == "" {
		return nil
	}
	globalLog := filepath.Join(home, ".bmad", "audit.jsonl")

	projectLog := ""
	if len(args) > 0 {
		arg := args[0]
		if strings.HasSuffix(arg, "bmad-progress.json") {
			projectLog = filepath.Join(filepath.Dir(arg), "bmad-audit.jsonl")
		} else if info, err := os.Stat(arg); err == nil && info.IsDir() {
			projectLog = filepath.Join(arg, "bmad-audit.jsonl")
		}
	}
	if projectLog == "" {
		projectLog = globalLog // fallback to global only
	}

	return infrastructure.NewJSONLLogWriter(projectLog, globalLog)
}

func exitOnError(err error) {
	if err != nil {
		log.Error(err.Error())
		os.Exit(1)
	}
}
