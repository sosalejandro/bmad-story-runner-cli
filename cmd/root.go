package cmd

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"
	"go.uber.org/zap"

	"github.com/sosalejandro/bmad-story-runner-cli/domain"
	"github.com/sosalejandro/bmad-story-runner-cli/infrastructure"
)

var (
	log   *zap.Logger
	noLog bool

	// disableStreamTap is flipped on by tests that don't want runWithLogging
	// to swap os.Stdout/os.Stderr (which would swallow the testing harness's
	// own output). Production code never touches it.
	disableStreamTap bool
)

func NewRootCmd(logger *zap.Logger) *cobra.Command {
	log = logger

	root := &cobra.Command{
		Use:     "bmad",
		Short:   "BMAD Story Runner CLI — manage bmad-progress.json without ad-hoc scripts",
		Version: VersionString(),
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
	// Wire `-v` as the short flag for `--version`. Cobra wires only the
	// long form by default once Version is set; users (and CI scripts)
	// expect both forms to work.
	root.Flags().BoolP("version", "v", false, "print the bmad-cli version, commit, and build date")

	root.PersistentFlags().BoolVar(&noLog, "no-log", false, "Disable audit logging for this invocation")
	// --json: opt-in machine-readable output across every command. See
	// cmd/jsonout.go for the v1 envelope contract. Commands honor this by
	// branching on the `jsonOutput` package variable.
	root.PersistentFlags().BoolVar(&jsonOutput, "json", false, "Emit output as a JSON envelope (schema v1)")

	root.AddCommand(
		newInitCmd(),        // v6 — sqlite init
		newInstallCmd(),     // v6 — drop embedded agents + skills into .claude/
		newConfigCmd(),      // v6 — runtime config get/set
		newStoryCmd(),       // v6 — story namespace
		newEnvCmd(),         // v6 — env namespace (port-pool + sweeper)
		newWorktreeCmd(),    // v6 — worktree namespace
		newDepguardCmd(),    // v6 — depguard flip ratchet
		newGateCmd(),        // v6 — gate namespace (v4 surface preserved)
		newDispatchCmd(),    // v6 — dispatch record (§12.7 token cost)
		newRenderCmd(),      // v6 — prompt template render
		newSystemCheckCmd(), // v6 — host resource probe
		newDoctorCmd(),      // v6 — env self-check (binary/atlas/db/schema/exit codes)
		newSprintCmd(),      // v6 — sprint namespace (plan/run/pause/resume/status)
		newMigrateCmd(),     // v6 — one-shot v4 progress.json → v6 sqlite import
		newStatusCmd(),
		newSetStatusCmd(),
		newSetCompleteCmd(),
		newBulkCompleteCmd(),
		newAddConcernsCmd(),
		newNextCmd(),
		newNextActionsAliasCmd(), // v6 — top-level alias for `bmad story next` (issue #71)
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

	// --help-json must be registered AFTER subcommands are added so the
	// tree walk it triggers sees the full surface. See cmd/help_json.go.
	registerHelpJSONFlag(root)

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

// runWithLogging executes the command function and writes a single audit log
// entry per invocation.
//
// Earlier versions emitted both a session_start and a command entry on every
// invocation, which produced two audit rows per `bmad` run because each
// process has its own PID and therefore matched as a "new session" by the
// PWD+PID check. Session correlation across invocations is a read-time
// concern; the embedded Session field on each command entry already carries
// PPID/PWD/User/Terminal, which is sufficient to group entries downstream.
// (closes #4)
func runWithLogging(cmd *cobra.Command, args []string, fn func() error) error {
	if noLog {
		return fn()
	}

	lw := getLogWriter(args)
	if lw == nil {
		return fn()
	}

	session := infrastructure.CollectSessionInfo(Version, CommitSHA)

	// Tap stdout/stderr through a byte-counter so the audit row records what
	// the command actually emitted. Without this, stdout_bytes / stderr_bytes
	// are always zero. (closes #5)
	var stdoutBytes, stderrBytes int64
	stopTap := func() {}
	if !disableStreamTap {
		stopTap = tapStdStreams(&stdoutBytes, &stderrBytes)
	}

	startTime := time.Now()

	// Run the actual command.
	err := fn()

	// Stop tapping and read totals before any post-exec output would
	// otherwise be attributed to the command we just ran.
	stopTap()

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
			ExitCode:    exitCode,
			StdoutBytes: stdoutBytes,
			StderrBytes: stderrBytes,
			Error:       &errMsg,
		}
	} else {
		entry.Result = &domain.ResultInfo{
			ExitCode:    0,
			StdoutBytes: stdoutBytes,
			StderrBytes: stderrBytes,
		}
	}

	lw.WriteEntry(entry) //nolint:errcheck
	return err
}

// tapStdStreams replaces os.Stdout and os.Stderr with pipe writers so the
// bytes the wrapped command emits flow through goroutines that count and
// forward them to the original streams. The returned stop() function closes
// the pipes, waits for the drain goroutines to finish, restores os.Stdout
// and os.Stderr, and is safe to call exactly once.
func tapStdStreams(stdoutBytes, stderrBytes *int64) func() {
	origOut, origErr := os.Stdout, os.Stderr

	outR, outW, errOut := os.Pipe()
	if errOut != nil {
		// Fall back to no-op on pipe failure rather than break the command.
		return func() {}
	}
	errR, errW, errErr := os.Pipe()
	if errErr != nil {
		outR.Close()
		outW.Close()
		return func() {}
	}

	os.Stdout = outW
	os.Stderr = errW

	var wg sync.WaitGroup
	wg.Add(2)

	drain := func(r *os.File, dest io.Writer, n *int64) {
		defer wg.Done()
		buf := make([]byte, 32*1024)
		for {
			read, rerr := r.Read(buf)
			if read > 0 {
				if _, werr := dest.Write(buf[:read]); werr == nil {
					*n += int64(read)
				}
			}
			if rerr != nil {
				return
			}
		}
	}
	go drain(outR, origOut, stdoutBytes)
	go drain(errR, origErr, stderrBytes)

	var once sync.Once
	return func() {
		once.Do(func() {
			// Close write ends so the drain goroutines see io.EOF and exit.
			_ = outW.Close()
			_ = errW.Close()
			wg.Wait()
			_ = outR.Close()
			_ = errR.Close()
			os.Stdout = origOut
			os.Stderr = origErr
		})
	}
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
