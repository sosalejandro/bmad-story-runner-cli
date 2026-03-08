# Logging & Telemetry Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add structured audit logging, performance telemetry, and an `exec` wrapper to the bmad CLI so every invocation is tracked and workaround patterns can be identified.

**Architecture:** Middleware-based instrumentation in `cmd/root.go` wraps all commands. A `LogWriter` port with a JSONL infrastructure adapter handles dual-write to project + global logs. `bmad exec` wraps child processes with transparent stdin/stdout/stderr passthrough. `bmad log` subcommands query and analyze log data.

**Tech Stack:** Go stdlib (`runtime`, `syscall`, `os`, `encoding/json`, `compress/gzip`, `math`, `sort`), existing `go.uber.org/zap`, existing `github.com/spf13/cobra`.

**Design doc:** `docs/plans/2026-03-08-logging-telemetry-design.md`

---

### Task 0: Fix relative path resolution across all commands

Only `init.go` calls `filepath.Abs()` — all other commands pass raw `args[0]` which breaks when Claude uses relative paths. Fix this at the root level.

**Files:**
- Modify: `cmd/root.go`
- Modify: `cmd/util.go`

**Step 1: Add a path resolution helper to `cmd/util.go`**

```go
package cmd

import (
	"path/filepath"
	"strings"
)

func repeat(s string, n int) string {
	return strings.Repeat(s, n)
}

// absPath resolves a path argument to absolute. Returns the path unchanged if
// resolution fails (let downstream report the real error).
func absPath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	return abs
}
```

**Step 2: Add a Cobra PersistentPreRun hook in `cmd/root.go` that resolves args[0]**

In `NewRootCmd`, after creating the root command, add a `PersistentPreRun` that resolves the first argument if it looks like a path (contains `/` or `.`):

```go
root.PersistentPreRun = func(cmd *cobra.Command, args []string) {
	if len(args) > 0 && looksLikePath(args[0]) {
		args[0] = absPath(args[0])
	}
}
```

Add to `cmd/util.go`:

```go
// looksLikePath returns true if the string looks like a file/directory path.
func looksLikePath(s string) bool {
	return strings.Contains(s, "/") || strings.Contains(s, ".json") ||
		strings.Contains(s, ".md") || strings.HasPrefix(s, ".")
}
```

**Step 3: Remove the redundant `filepath.Abs` call from `cmd/init.go:19`**

The `init` command already does this — remove it since the root hook handles it now. Change `init.go` line 19 from:
```go
docsFolder, err := filepath.Abs(args[0])
exitOnError(err)
```
to just:
```go
docsFolder := args[0]
```

**Step 4: Verify the build compiles**

Run: `cd /home/as-main/Documents/projects/bmad-story-runner-cli && go build ./...`
Expected: Clean build, no errors.

**Step 5: Commit**

```bash
git add cmd/util.go cmd/root.go cmd/init.go
git commit -m "fix: resolve relative paths to absolute in all commands

Adds PersistentPreRun hook that calls filepath.Abs on path arguments.
Fixes errors when Claude passes relative paths like docs/features/.../bmad-progress.json."
```

---

### Task 1: Domain log types

Pure types with zero I/O. These define the log entry schema.

**Files:**
- Create: `domain/log.go`

**Step 1: Write the failing test**

Create `domain/log_test.go`:

```go
package domain

import (
	"testing"
	"time"
)

func TestLogEntryTypeConstants(t *testing.T) {
	if EntryTypeCommand != "command" {
		t.Errorf("EntryTypeCommand = %q, want %q", EntryTypeCommand, "command")
	}
	if EntryTypeExec != "exec" {
		t.Errorf("EntryTypeExec = %q, want %q", EntryTypeExec, "exec")
	}
	if EntryTypeSessionStart != "session_start" {
		t.Errorf("EntryTypeSessionStart = %q, want %q", EntryTypeSessionStart, "session_start")
	}
}

func TestLogEntryIsNewSession(t *testing.T) {
	prev := &LogEntry{
		Session: SessionInfo{PWD: "/project-a", PID: 100},
	}
	same := &LogEntry{
		Session: SessionInfo{PWD: "/project-a", PID: 100},
	}
	diff := &LogEntry{
		Session: SessionInfo{PWD: "/project-b", PID: 200},
	}

	if prev.IsNewSession(same) {
		t.Error("same pwd+pid should not be a new session")
	}
	if !prev.IsNewSession(diff) {
		t.Error("different pwd+pid should be a new session")
	}
}

func TestPerformanceStatsTotalFromPhases(t *testing.T) {
	ps := PerformanceStats{
		IOReadMs:  4.0,
		IOWriteMs: 2.0,
		ParseMs:   3.0,
		LogicMs:   5.0,
	}
	ps.ComputeTotal()
	if ps.TotalMs != 14.0 {
		t.Errorf("TotalMs = %f, want 14.0", ps.TotalMs)
	}
}

func TestSessionInfoMatch(t *testing.T) {
	a := SessionInfo{PWD: "/a", PID: 1}
	b := SessionInfo{PWD: "/a", PID: 1}
	c := SessionInfo{PWD: "/b", PID: 2}

	if !a.Match(b) {
		t.Error("identical sessions should match")
	}
	if a.Match(c) {
		t.Error("different sessions should not match")
	}
}

func TestNewSessionEntry(t *testing.T) {
	info := SessionInfo{
		PWD:         "/test",
		PID:         42,
		BmadVersion: "0.4.0",
	}
	entry := NewSessionStartEntry(info)
	if entry.Type != EntryTypeSessionStart {
		t.Errorf("Type = %q, want %q", entry.Type, EntryTypeSessionStart)
	}
	if entry.Timestamp.IsZero() {
		t.Error("Timestamp should not be zero")
	}
	_ = time.Now() // reference import
}
```

**Step 2: Run test to verify it fails**

Run: `cd /home/as-main/Documents/projects/bmad-story-runner-cli && go test ./domain/ -v -run TestLog`
Expected: FAIL — types not defined yet.

**Step 3: Write the implementation**

Create `domain/log.go`:

```go
package domain

import "time"

// Entry type constants.
const (
	EntryTypeCommand      = "command"
	EntryTypeExec         = "exec"
	EntryTypeSessionStart = "session_start"
)

// LogEntry is a single audit log record.
type LogEntry struct {
	Type        string           `json:"type"`
	Version     string           `json:"version"`
	Timestamp   time.Time        `json:"timestamp"`
	Session     SessionInfo      `json:"session"`
	Command     *CommandInfo     `json:"command,omitempty"`
	Result      *ResultInfo      `json:"result,omitempty"`
	Performance *PerformanceStats `json:"performance,omitempty"`
	Memory      *MemoryStats     `json:"memory,omitempty"`
	IO          *IOStats         `json:"io,omitempty"`
	Exec        *ExecInfo        `json:"exec,omitempty"`
}

// SessionInfo identifies the terminal session and environment.
type SessionInfo struct {
	PWD         string `json:"pwd"`
	Terminal    string `json:"terminal"`
	PID         int    `json:"pid"`
	PPID        int    `json:"ppid"`
	User        string `json:"user"`
	Hostname    string `json:"hostname"`
	OS          string `json:"os"`
	Arch        string `json:"arch"`
	GoVersion   string `json:"go_version"`
	BmadVersion string `json:"bmad_version"`
	BmadCommit  string `json:"bmad_commit"`
}

// Match returns true if both sessions have the same PWD and PID.
func (s SessionInfo) Match(other SessionInfo) bool {
	return s.PWD == other.PWD && s.PID == other.PID
}

// CommandInfo describes the bmad command that was invoked.
type CommandInfo struct {
	Name  string            `json:"name"`
	Args  []string          `json:"args"`
	Raw   string            `json:"raw"`
	Flags map[string]string `json:"flags,omitempty"`
}

// ResultInfo captures command outcome.
type ResultInfo struct {
	ExitCode    int     `json:"exit_code"`
	StdoutBytes int64   `json:"stdout_bytes"`
	StderrBytes int64   `json:"stderr_bytes"`
	Error       *string `json:"error"`
	ErrorType   *string `json:"error_type"`
}

// PerformanceStats captures timing breakdown.
type PerformanceStats struct {
	TotalMs     float64 `json:"total_ms"`
	IOReadMs    float64 `json:"io_read_ms"`
	IOWriteMs   float64 `json:"io_write_ms"`
	ParseMs     float64 `json:"parse_ms"`
	LogicMs     float64 `json:"logic_ms"`
	WallClockNs int64   `json:"wall_clock_ns"`
}

// ComputeTotal sums the phase durations into TotalMs.
func (p *PerformanceStats) ComputeTotal() {
	p.TotalMs = p.IOReadMs + p.IOWriteMs + p.ParseMs + p.LogicMs
}

// MemoryStats captures Go runtime memory metrics.
type MemoryStats struct {
	HeapAllocBytes  uint64 `json:"heap_alloc_bytes"`
	HeapSysBytes    uint64 `json:"heap_sys_bytes"`
	TotalAllocBytes uint64 `json:"total_alloc_bytes"`
	NumGC           uint32 `json:"num_gc"`
	GCPauseTotalNs  uint64 `json:"gc_pause_total_ns"`
	GCPauseLastNs   uint64 `json:"gc_pause_last_ns"`
	Goroutines      int    `json:"goroutines"`
	StackInuseBytes uint64 `json:"stack_inuse_bytes"`
	PeakRSSBytes    uint64 `json:"peak_rss_bytes"`
	Mallocs         uint64 `json:"mallocs"`
	Frees           uint64 `json:"frees"`
}

// IOStats captures domain-level I/O counters.
type IOStats struct {
	FilesRead        int   `json:"files_read"`
	FilesWritten     int   `json:"files_written"`
	BytesRead        int64 `json:"bytes_read"`
	BytesWritten     int64 `json:"bytes_written"`
	StoriesProcessed int   `json:"stories_processed"`
}

// ExecInfo captures child process metrics for bmad exec.
type ExecInfo struct {
	ChildCommand      string      `json:"child_command"`
	ChildArgs         []string    `json:"child_args"`
	ChildExitCode     int         `json:"child_exit_code"`
	ChildDurationMs   float64     `json:"child_duration_ms"`
	ChildStdoutBytes  int64       `json:"child_stdout_bytes"`
	ChildStderrBytes  int64       `json:"child_stderr_bytes"`
	ChildPeakRSSBytes uint64      `json:"child_peak_rss_bytes"`
	ChildUserTimeMs   float64     `json:"child_user_time_ms"`
	ChildSysTimeMs    float64     `json:"child_sys_time_ms"`
	Context           ExecContext `json:"context"`
}

// ExecContext links exec calls to the preceding bmad command.
type ExecContext struct {
	PreviousBmadCommand string `json:"previous_bmad_command"`
	Reason              string `json:"reason"`
}

// IsNewSession returns true if this entry's session differs from prev.
func (e *LogEntry) IsNewSession(prev *LogEntry) bool {
	return !e.Session.Match(prev.Session)
}

// NewSessionStartEntry creates a session_start log entry.
func NewSessionStartEntry(session SessionInfo) *LogEntry {
	return &LogEntry{
		Type:      EntryTypeSessionStart,
		Version:   "1.0.0",
		Timestamp: time.Now().UTC(),
		Session:   session,
	}
}

// NewCommandEntry creates a command log entry.
func NewCommandEntry(session SessionInfo, cmd *CommandInfo) *LogEntry {
	return &LogEntry{
		Type:      EntryTypeCommand,
		Version:   "1.0.0",
		Timestamp: time.Now().UTC(),
		Session:   session,
		Command:   cmd,
	}
}

// NewExecEntry creates an exec log entry.
func NewExecEntry(session SessionInfo, cmd *CommandInfo, exec *ExecInfo) *LogEntry {
	return &LogEntry{
		Type:      EntryTypeExec,
		Version:   "1.0.0",
		Timestamp: time.Now().UTC(),
		Session:   session,
		Command:   cmd,
		Exec:      exec,
	}
}
```

**Step 4: Run tests to verify they pass**

Run: `cd /home/as-main/Documents/projects/bmad-story-runner-cli && go test ./domain/ -v -run TestLog`
Expected: All PASS.

**Step 5: Commit**

```bash
git add domain/log.go domain/log_test.go
git commit -m "feat: add domain log types for audit logging

LogEntry, SessionInfo, CommandInfo, ResultInfo, PerformanceStats,
MemoryStats, IOStats, ExecInfo types with constructors and helpers."
```

---

### Task 2: Build info injection via ldflags

Set up version and commit SHA injection at build time.

**Files:**
- Create: `cmd/version.go`
- Modify: `cmd/bmad/main.go`

**Step 1: Create `cmd/version.go` with build-time variables**

```go
package cmd

// Set via ldflags at build time:
//   go build -ldflags "-X github.com/sosalejandro/bmad-story-runner-cli/cmd.Version=0.4.0
//     -X github.com/sosalejandro/bmad-story-runner-cli/cmd.CommitSHA=$(git rev-parse --short HEAD)"
var (
	Version   = "dev"
	CommitSHA = "unknown"
)
```

**Step 2: Verify build compiles**

Run: `cd /home/as-main/Documents/projects/bmad-story-runner-cli && go build ./...`
Expected: Clean build.

**Step 3: Verify ldflags injection works**

Run: `cd /home/as-main/Documents/projects/bmad-story-runner-cli && go build -ldflags "-X github.com/sosalejandro/bmad-story-runner-cli/cmd.Version=test -X github.com/sosalejandro/bmad-story-runner-cli/cmd.CommitSHA=abc123" -o /tmp/bmad-test ./cmd/bmad && echo "build ok"`
Expected: `build ok`

**Step 4: Commit**

```bash
git add cmd/version.go
git commit -m "feat: add build-time version and commit SHA via ldflags"
```

---

### Task 3: Process metrics collector

Infrastructure adapter that collects Go runtime memory stats and Linux peak RSS.

**Files:**
- Create: `infrastructure/process_metrics.go`
- Create: `infrastructure/process_metrics_test.go`

**Step 1: Write the failing test**

Create `infrastructure/process_metrics_test.go`:

```go
package infrastructure

import (
	"testing"

	"github.com/sosalejandro/bmad-story-runner-cli/domain"
)

func TestCollectMemoryStats(t *testing.T) {
	stats := CollectMemoryStats()

	if stats.HeapAllocBytes == 0 {
		t.Error("HeapAllocBytes should be > 0")
	}
	if stats.HeapSysBytes == 0 {
		t.Error("HeapSysBytes should be > 0")
	}
	if stats.Goroutines == 0 {
		t.Error("Goroutines should be > 0")
	}
	if stats.StackInuseBytes == 0 {
		t.Error("StackInuseBytes should be > 0")
	}
}

func TestCollectSessionInfo(t *testing.T) {
	info := CollectSessionInfo("0.4.0", "abc123")

	if info.PWD == "" {
		t.Error("PWD should not be empty")
	}
	if info.PID == 0 {
		t.Error("PID should not be 0")
	}
	if info.OS == "" {
		t.Error("OS should not be empty")
	}
	if info.BmadVersion != "0.4.0" {
		t.Errorf("BmadVersion = %q, want %q", info.BmadVersion, "0.4.0")
	}
}

func TestMemoryStatsDelta(t *testing.T) {
	before := domain.MemoryStats{
		HeapAllocBytes:  100,
		TotalAllocBytes: 200,
		NumGC:           1,
		Mallocs:         50,
		Frees:           30,
	}
	after := domain.MemoryStats{
		HeapAllocBytes:  250,
		TotalAllocBytes: 500,
		NumGC:           3,
		Mallocs:         120,
		Frees:           80,
	}
	// Delta should represent the "after" snapshot since that's what we report
	// (the final state of the process).
	_ = before
	_ = after
}
```

**Step 2: Run test to verify it fails**

Run: `cd /home/as-main/Documents/projects/bmad-story-runner-cli && go test ./infrastructure/ -v -run TestCollect`
Expected: FAIL — functions not defined.

**Step 3: Write the implementation**

Create `infrastructure/process_metrics.go`:

```go
package infrastructure

import (
	"bufio"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"

	"github.com/sosalejandro/bmad-story-runner-cli/domain"
)

// CollectMemoryStats reads Go runtime memory statistics and Linux peak RSS.
func CollectMemoryStats() domain.MemoryStats {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	return domain.MemoryStats{
		HeapAllocBytes:  m.HeapAlloc,
		HeapSysBytes:    m.HeapSys,
		TotalAllocBytes: m.TotalAlloc,
		NumGC:           m.NumGC,
		GCPauseTotalNs:  m.PauseTotalNs,
		GCPauseLastNs:   m.PauseNs[(m.NumGC+255)%256],
		Goroutines:      runtime.NumGoroutine(),
		StackInuseBytes: m.StackInuse,
		PeakRSSBytes:    readPeakRSS(),
		Mallocs:         m.Mallocs,
		Frees:           m.Frees,
	}
}

// CollectSessionInfo gathers environment metadata for the current process.
func CollectSessionInfo(version, commit string) domain.SessionInfo {
	pwd, _ := os.Getwd()
	hostname, _ := os.Hostname()
	username := ""
	if u, err := user.Current(); err == nil {
		username = u.Username
	}

	return domain.SessionInfo{
		PWD:         pwd,
		Terminal:    detectTerminal(),
		PID:         os.Getpid(),
		PPID:        os.Getppid(),
		User:        username,
		Hostname:    hostname,
		OS:          runtime.GOOS,
		Arch:        runtime.GOARCH,
		GoVersion:   runtime.Version(),
		BmadVersion: version,
		BmadCommit:  commit,
	}
}

// CollectChildMetrics extracts CPU time and peak RSS from a completed child process.
func CollectChildMetrics(state *os.ProcessState) (userTimeMs, sysTimeMs float64, peakRSSBytes uint64) {
	if state == nil {
		return 0, 0, 0
	}
	usage, ok := state.SysUsage().(*syscall.Rusage)
	if !ok || usage == nil {
		return 0, 0, 0
	}
	userTimeMs = float64(usage.Utime.Sec)*1000 + float64(usage.Utime.Usec)/1000
	sysTimeMs = float64(usage.Stime.Sec)*1000 + float64(usage.Stime.Usec)/1000
	peakRSSBytes = uint64(usage.Maxrss) * 1024 // Maxrss is in KB on Linux
	return
}

// readPeakRSS reads VmHWM from /proc/self/status (Linux only).
// Returns 0 on non-Linux platforms or on error.
func readPeakRSS() uint64 {
	if runtime.GOOS != "linux" {
		return 0
	}
	f, err := os.Open("/proc/self/status")
	if err != nil {
		return 0
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "VmHWM:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				kb, err := strconv.ParseUint(fields[1], 10, 64)
				if err == nil {
					return kb * 1024
				}
			}
		}
	}
	return 0
}

// detectTerminal tries to identify the parent process name.
func detectTerminal() string {
	ppid := os.Getppid()
	comm, err := os.ReadFile(fmt.Sprintf("/proc/%d/comm", ppid))
	if err != nil {
		return "unknown"
	}
	name := strings.TrimSpace(string(comm))
	// Map known parent process names to friendly labels.
	switch {
	case strings.Contains(name, "claude"):
		return "claude-code"
	case strings.Contains(name, "code"):
		return "vscode"
	default:
		return name
	}
}

// PhaseTimer tracks time spent in named execution phases.
type PhaseTimer struct {
	phases  map[string]int64 // phase name -> nanoseconds
	current string
	start   int64
}

// NewPhaseTimer creates a new phase timer.
func NewPhaseTimer() *PhaseTimer {
	return &PhaseTimer{
		phases: make(map[string]int64),
	}
}

// StartPhase begins timing a named phase. Ends the previous phase if one is active.
func (t *PhaseTimer) StartPhase(name string) {
	now := nanotime()
	if t.current != "" {
		t.phases[t.current] += now - t.start
	}
	t.current = name
	t.start = now
}

// EndPhase stops timing the current phase.
func (t *PhaseTimer) EndPhase() {
	if t.current == "" {
		return
	}
	now := nanotime()
	t.phases[t.current] += now - t.start
	t.current = ""
}

// ToPerformanceStats converts phase timings to a PerformanceStats struct.
func (t *PhaseTimer) ToPerformanceStats(wallClockNs int64) domain.PerformanceStats {
	t.EndPhase() // close any open phase
	ms := func(ns int64) float64 { return float64(ns) / 1e6 }
	ps := domain.PerformanceStats{
		IOReadMs:    ms(t.phases["io_read"]),
		IOWriteMs:   ms(t.phases["io_write"]),
		ParseMs:     ms(t.phases["parse"]),
		LogicMs:     ms(t.phases["logic"]),
		WallClockNs: wallClockNs,
	}
	ps.TotalMs = float64(wallClockNs) / 1e6
	return ps
}

func nanotime() int64 {
	// Use filepath to avoid unused import. We actually need time here.
	_ = filepath.Base
	// Simplified: use runtime nanotime via time package.
	return runtimeNanotime()
}

func runtimeNanotime() int64 {
	// Use monotonic clock via time.Now().UnixNano()
	// This is sufficient for phase timing.
	return int64(0) // placeholder — will use time.Now().UnixNano()
}
```

**Wait — the nanotime approach is over-engineered.** Simplify. Replace the last part:

```go
// Replace the nanotime/runtimeNanotime functions with:
import "time"

func nanotime() int64 {
	return time.Now().UnixNano()
}
// Remove runtimeNanotime entirely.
```

**The complete file should use `time` in the import block (already present for the PhaseTimer). Remove the `filepath` import workaround and use `time.Now().UnixNano()` directly in `nanotime()`.**

**Step 4: Run tests to verify they pass**

Run: `cd /home/as-main/Documents/projects/bmad-story-runner-cli && go test ./infrastructure/ -v -run TestCollect`
Expected: All PASS.

**Step 5: Commit**

```bash
git add infrastructure/process_metrics.go infrastructure/process_metrics_test.go
git commit -m "feat: add process metrics collector

CollectMemoryStats reads Go runtime.MemStats and Linux peak RSS.
CollectSessionInfo gathers PWD, PID, hostname, Go/bmad versions.
CollectChildMetrics extracts Rusage from child processes.
PhaseTimer tracks io_read, io_write, parse, logic phase durations."
```

---

### Task 4: LogWriter port and JSONL adapter

The port interface and the infrastructure adapter that writes to both project and global JSONL files.

**Files:**
- Create: `application/ports/log_writer.go`
- Create: `infrastructure/jsonl_log_writer.go`
- Create: `infrastructure/jsonl_log_writer_test.go`

**Step 1: Write the port interface**

Create `application/ports/log_writer.go`:

```go
package ports

import "github.com/sosalejandro/bmad-story-runner-cli/domain"

// LogWriter handles audit log persistence.
type LogWriter interface {
	// WriteEntry appends a log entry to both project and global logs.
	WriteEntry(entry *domain.LogEntry) error

	// LastEntry reads the most recent entry from the project log.
	// Returns nil if the log is empty or does not exist.
	LastEntry() *domain.LogEntry

	// ReadEntries reads all entries from the specified log file.
	ReadEntries(path string) ([]*domain.LogEntry, error)

	// Rotate archives the current log to a gzipped timestamped file.
	Rotate(path string) error
}
```

**Step 2: Write the failing test for the JSONL adapter**

Create `infrastructure/jsonl_log_writer_test.go`:

```go
package infrastructure

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sosalejandro/bmad-story-runner-cli/domain"
)

func TestJSONLLogWriter_WriteAndRead(t *testing.T) {
	dir := t.TempDir()
	projectLog := filepath.Join(dir, "project", "bmad-audit.jsonl")
	globalLog := filepath.Join(dir, "global", "audit.jsonl")

	w := NewJSONLLogWriter(projectLog, globalLog)

	entry := &domain.LogEntry{
		Type:      domain.EntryTypeCommand,
		Version:   "1.0.0",
		Timestamp: time.Now().UTC(),
		Session:   domain.SessionInfo{PWD: "/test", PID: 42},
		Command:   &domain.CommandInfo{Name: "next", Args: []string{"p.json"}},
	}

	if err := w.WriteEntry(entry); err != nil {
		t.Fatalf("WriteEntry: %v", err)
	}

	// Verify project log exists and has one line.
	data, err := os.ReadFile(projectLog)
	if err != nil {
		t.Fatalf("reading project log: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}

	var decoded domain.LogEntry
	if err := json.Unmarshal([]byte(lines[0]), &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.Command.Name != "next" {
		t.Errorf("Command.Name = %q, want %q", decoded.Command.Name, "next")
	}

	// Verify global log also has the entry.
	globalData, err := os.ReadFile(globalLog)
	if err != nil {
		t.Fatalf("reading global log: %v", err)
	}
	if len(strings.TrimSpace(string(globalData))) == 0 {
		t.Error("global log should not be empty")
	}
}

func TestJSONLLogWriter_LastEntry(t *testing.T) {
	dir := t.TempDir()
	projectLog := filepath.Join(dir, "bmad-audit.jsonl")
	globalLog := filepath.Join(dir, "global", "audit.jsonl")

	w := NewJSONLLogWriter(projectLog, globalLog)

	// No entries yet.
	if last := w.LastEntry(); last != nil {
		t.Error("LastEntry should be nil for empty log")
	}

	// Write two entries.
	e1 := &domain.LogEntry{
		Type:    domain.EntryTypeCommand,
		Version: "1.0.0",
		Session: domain.SessionInfo{PWD: "/a", PID: 1},
		Command: &domain.CommandInfo{Name: "init"},
	}
	e2 := &domain.LogEntry{
		Type:    domain.EntryTypeCommand,
		Version: "1.0.0",
		Session: domain.SessionInfo{PWD: "/a", PID: 1},
		Command: &domain.CommandInfo{Name: "next"},
	}
	w.WriteEntry(e1)
	w.WriteEntry(e2)

	last := w.LastEntry()
	if last == nil {
		t.Fatal("LastEntry should not be nil")
	}
	if last.Command.Name != "next" {
		t.Errorf("LastEntry command = %q, want %q", last.Command.Name, "next")
	}
}

func TestJSONLLogWriter_Rotate(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "bmad-audit.jsonl")
	globalLog := filepath.Join(dir, "global", "audit.jsonl")

	w := NewJSONLLogWriter(logPath, globalLog)

	entry := &domain.LogEntry{
		Type:    domain.EntryTypeCommand,
		Version: "1.0.0",
		Session: domain.SessionInfo{PWD: "/test", PID: 1},
	}
	w.WriteEntry(entry)

	if err := w.Rotate(logPath); err != nil {
		t.Fatalf("Rotate: %v", err)
	}

	// Original file should be empty or gone, archive should exist.
	matches, _ := filepath.Glob(filepath.Join(dir, "audit-*.jsonl.gz"))
	if len(matches) == 0 {
		t.Error("no archive file found after rotation")
	}

	// Fresh log should be empty.
	data, _ := os.ReadFile(logPath)
	if len(data) != 0 {
		t.Error("log file should be empty after rotation")
	}
}

func TestJSONLLogWriter_ReadEntries(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "bmad-audit.jsonl")
	globalLog := filepath.Join(dir, "global", "audit.jsonl")

	w := NewJSONLLogWriter(logPath, globalLog)

	for i := 0; i < 3; i++ {
		w.WriteEntry(&domain.LogEntry{
			Type:    domain.EntryTypeCommand,
			Version: "1.0.0",
			Session: domain.SessionInfo{PWD: "/test", PID: 1},
			Command: &domain.CommandInfo{Name: "status"},
		})
	}

	entries, err := w.ReadEntries(logPath)
	if err != nil {
		t.Fatalf("ReadEntries: %v", err)
	}
	if len(entries) != 3 {
		t.Errorf("got %d entries, want 3", len(entries))
	}
}
```

**Step 3: Run test to verify it fails**

Run: `cd /home/as-main/Documents/projects/bmad-story-runner-cli && go test ./infrastructure/ -v -run TestJSONLLogWriter`
Expected: FAIL — `NewJSONLLogWriter` not defined.

**Step 4: Write the implementation**

Create `infrastructure/jsonl_log_writer.go`:

```go
package infrastructure

import (
	"bufio"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/sosalejandro/bmad-story-runner-cli/domain"
)

// JSONLLogWriter writes audit log entries to JSONL files.
// It dual-writes to both a project-specific log and a global log.
type JSONLLogWriter struct {
	projectLogPath string
	globalLogPath  string
}

// NewJSONLLogWriter creates a log writer that writes to both paths.
func NewJSONLLogWriter(projectLogPath, globalLogPath string) *JSONLLogWriter {
	return &JSONLLogWriter{
		projectLogPath: projectLogPath,
		globalLogPath:  globalLogPath,
	}
}

// WriteEntry appends a JSON-encoded log entry to both log files.
func (w *JSONLLogWriter) WriteEntry(entry *domain.LogEntry) error {
	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshalling log entry: %w", err)
	}
	data = append(data, '\n')

	if err := w.appendToFile(w.projectLogPath, data); err != nil {
		return fmt.Errorf("writing project log: %w", err)
	}
	if err := w.appendToFile(w.globalLogPath, data); err != nil {
		// Log to project succeeded — don't fail the whole operation
		// for a global log write failure. Best-effort.
		return nil
	}
	return nil
}

// LastEntry reads the most recent entry from the project log.
func (w *JSONLLogWriter) LastEntry() *domain.LogEntry {
	entries, err := w.ReadEntries(w.projectLogPath)
	if err != nil || len(entries) == 0 {
		return nil
	}
	return entries[len(entries)-1]
}

// ReadEntries reads all entries from the specified JSONL file.
func (w *JSONLLogWriter) ReadEntries(path string) ([]*domain.LogEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("opening log file: %w", err)
	}
	defer f.Close()

	var entries []*domain.LogEntry
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024) // 1MB max line
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var entry domain.LogEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			continue // skip malformed lines
		}
		entries = append(entries, &entry)
	}
	return entries, scanner.Err()
}

// Rotate archives the log file to a gzipped timestamped file and creates a fresh empty log.
func (w *JSONLLogWriter) Rotate(path string) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil // nothing to rotate
	}

	dir := filepath.Dir(path)
	archiveName := fmt.Sprintf("audit-%s.jsonl.gz", time.Now().UTC().Format("2006-01-02"))
	archivePath := filepath.Join(dir, archiveName)

	// Read current log.
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading log for rotation: %w", err)
	}

	// Write gzipped archive.
	af, err := os.Create(archivePath)
	if err != nil {
		return fmt.Errorf("creating archive: %w", err)
	}
	gz := gzip.NewWriter(af)
	if _, err := gz.Write(data); err != nil {
		gz.Close()
		af.Close()
		return fmt.Errorf("writing archive: %w", err)
	}
	gz.Close()
	af.Close()

	// Truncate the original log.
	return os.WriteFile(path, nil, 0644)
}

// appendToFile creates parent directories and appends data to a file.
func (w *JSONLLogWriter) appendToFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(data)
	return err
}
```

**Step 5: Run tests to verify they pass**

Run: `cd /home/as-main/Documents/projects/bmad-story-runner-cli && go test ./infrastructure/ -v -run TestJSONLLogWriter`
Expected: All PASS.

**Step 6: Commit**

```bash
git add application/ports/log_writer.go infrastructure/jsonl_log_writer.go infrastructure/jsonl_log_writer_test.go
git commit -m "feat: add LogWriter port and JSONL dual-write adapter

Writes to project log (<docs-folder>/bmad-audit.jsonl) and global log
(~/.bmad/audit.jsonl). Supports reading entries, last-entry lookup,
and gzipped log rotation."
```

---

### Task 5: Logging middleware in root.go

The cross-cutting concern that wraps every command with telemetry.

**Files:**
- Modify: `cmd/root.go`
- Modify: `cmd/bmad/main.go`

**Step 1: Modify `cmd/bmad/main.go` to ensure `~/.bmad/` exists and pass build info**

```go
package main

import (
	"os"
	"path/filepath"

	"go.uber.org/zap"

	"github.com/sosalejandro/bmad-story-runner-cli/cmd"
)

func main() {
	logger, err := zap.NewProduction()
	if err != nil {
		panic("failed to initialize logger: " + err.Error())
	}
	defer logger.Sync() //nolint:errcheck

	// Ensure global log directory exists.
	home, _ := os.UserHomeDir()
	if home != "" {
		os.MkdirAll(filepath.Join(home, ".bmad"), 0755) //nolint:errcheck
	}

	root := cmd.NewRootCmd(logger)
	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}
```

**Step 2: Modify `cmd/root.go` to add logging middleware and global flags**

Replace `cmd/root.go` entirely:

```go
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
	log       *zap.Logger
	noLog     bool
	logWriter *infrastructure.JSONLLogWriter
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
		newInitCmd(),
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
		newLogCmd(),
	)

	// Wrap all leaf commands with logging middleware.
	if !noLog {
		wrapCommandsWithLogging(root)
	}

	return root
}

// wrapCommandsWithLogging wraps every leaf command's Run/RunE with audit logging.
func wrapCommandsWithLogging(root *cobra.Command) {
	for _, cmd := range root.Commands() {
		if cmd.HasSubCommands() {
			// Don't wrap parent commands (like "log"), wrap their children.
			wrapCommandsWithLogging(cmd)
			continue
		}
		// Skip the log subcommands themselves to avoid infinite recursion.
		if isLogCommand(cmd) {
			continue
		}
		wrapSingleCommand(cmd)
	}
}

func isLogCommand(cmd *cobra.Command) bool {
	for p := cmd; p != nil; p = p.Parent() {
		if p.Name() == "log" {
			return true
		}
	}
	return false
}

func wrapSingleCommand(cmd *cobra.Command) {
	if cmd.RunE != nil {
		original := cmd.RunE
		cmd.RunE = func(c *cobra.Command, args []string) error {
			return runWithLogging(c, args, func() error { return original(c, args) })
		}
	} else if cmd.Run != nil {
		original := cmd.Run
		cmd.Run = func(c *cobra.Command, args []string) {
			runWithLogging(c, args, func() error { original(c, args); return nil })
		}
	}
}

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
		lw.WriteEntry(domain.NewSessionStartEntry(session))
	}

	// Collect pre-execution memory.
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

	lw.WriteEntry(entry)
	return err
}

// getLogWriter creates a log writer based on the command args.
// It tries to detect the project log path from the progress file or docs folder argument.
func getLogWriter(args []string) *infrastructure.JSONLLogWriter {
	home, _ := os.UserHomeDir()
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
```

**Step 3: Verify the build compiles**

Run: `cd /home/as-main/Documents/projects/bmad-story-runner-cli && go build ./...`
Expected: Clean build (will fail until `newExecCmd` and `newLogCmd` exist — create stubs if needed, or implement tasks 6 and 7 first).

**Note:** This step depends on Tasks 6 and 7. If building incrementally, create temporary stubs:

```go
// cmd/exec.go (stub)
package cmd
import "github.com/spf13/cobra"
func newExecCmd() *cobra.Command {
	return &cobra.Command{Use: "exec", Short: "Run a command with audit logging (coming soon)"}
}

// cmd/log.go (stub)
package cmd
import "github.com/spf13/cobra"
func newLogCmd() *cobra.Command {
	return &cobra.Command{Use: "log", Short: "View and manage audit logs (coming soon)"}
}
```

**Step 4: Commit**

```bash
git add cmd/root.go cmd/bmad/main.go cmd/exec.go cmd/log.go
git commit -m "feat: add audit logging middleware to all commands

Every bmad command now writes a structured JSONL entry with timing,
memory stats, and session info. Dual-writes to project + global logs.
--no-log flag disables logging. Stubs for exec and log commands."
```

---

### Task 6: `bmad exec` command

The escape-hatch wrapper for non-native operations.

**Files:**
- Replace stub: `cmd/exec.go`

**Step 1: Write the implementation**

Replace `cmd/exec.go`:

```go
package cmd

import (
	"fmt"
	"os"
	"os/exec"
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

			// Run the child process.
			startTime := time.Now()
			child := exec.Command(childCmd, childArgs...)
			child.Stdin = os.Stdin
			child.Stdout = os.Stdout
			child.Stderr = os.Stderr

			err := child.Run()
			elapsed := time.Since(startTime)

			childExitCode := 0
			if err != nil {
				if exitErr, ok := err.(*exec.ExitError); ok {
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
				entry.Result = &domain.ResultInfo{ExitCode: childExitCode}

				lw := getLogWriter(nil)
				if lw != nil {
					lw.WriteEntry(entry)
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
```

**Step 2: Verify the build compiles**

Run: `cd /home/as-main/Documents/projects/bmad-story-runner-cli && go build ./...`
Expected: Clean build.

**Step 3: Smoke test**

Run: `cd /home/as-main/Documents/projects/bmad-story-runner-cli && go run ./cmd/bmad exec --reason "test" -- echo hello`
Expected: Prints `hello` to stdout, exits 0.

**Step 4: Commit**

```bash
git add cmd/exec.go
git commit -m "feat: add bmad exec command for wrapping non-native operations

Transparent stdin/stdout/stderr passthrough, child process telemetry
(CPU time, peak RSS, duration), --reason flag for pattern analysis,
auto-detected --context from previous log entry."
```

---

### Task 7: `bmad log` subcommand family

The log parent command and its four children: show, stats, patterns, rotate.

**Files:**
- Replace stub: `cmd/log.go`
- Create: `cmd/log_show.go`
- Create: `cmd/log_stats.go`
- Create: `cmd/log_patterns.go`
- Create: `cmd/log_rotate.go`
- Create: `application/log_stats.go`
- Create: `application/log_stats_test.go`

**Step 1: Create the log parent command**

Replace `cmd/log.go`:

```go
package cmd

import "github.com/spf13/cobra"

func newLogCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "log",
		Short: "View and manage audit logs",
		Long:  "Query, analyze, and rotate bmad audit logs.",
	}

	cmd.AddCommand(
		newLogShowCmd(),
		newLogStatsCmd(),
		newLogPatternsCmd(),
		newLogRotateCmd(),
	)

	return cmd
}
```

**Step 2: Create `application/log_stats.go` — the stats aggregation use case**

```go
package application

import (
	"math"
	"sort"

	"github.com/sosalejandro/bmad-story-runner-cli/domain"
)

// CommandStats holds aggregated metrics for a single command name.
type CommandStats struct {
	Name         string
	Count        int
	AvgMs        float64
	P50Ms        float64
	P95Ms        float64
	P99Ms        float64
	AvgHeapKB    float64
	MaxPeakRSSMB float64
	Errors       int
}

// ExecReasonStats holds aggregated metrics for a single exec reason.
type ExecReasonStats struct {
	Reason     string
	Count      int
	AvgMs      float64
	AvgRSSMB   float64
	TotalTimeS float64
}

// StatsReport is the output of log stats aggregation.
type StatsReport struct {
	Commands      []CommandStats
	ExecReasons   []ExecReasonStats
	TotalCommands int
	TotalExecs    int
	TotalErrors   int
	Sessions      int
}

// ComputeStats aggregates log entries into a stats report.
func ComputeStats(entries []*domain.LogEntry) *StatsReport {
	report := &StatsReport{}

	// Group entries by command name.
	cmdEntries := make(map[string][]*domain.LogEntry)
	reasonEntries := make(map[string][]*domain.LogEntry)
	sessionKeys := make(map[string]bool)

	for _, e := range entries {
		if e.Type == domain.EntryTypeSessionStart {
			key := e.Session.PWD + "|" + string(rune(e.Session.PID))
			sessionKeys[key] = true
			continue
		}
		if e.Command == nil {
			continue
		}

		name := e.Command.Name
		cmdEntries[name] = append(cmdEntries[name], e)

		if e.Type == domain.EntryTypeCommand {
			report.TotalCommands++
		}
		if e.Type == domain.EntryTypeExec {
			report.TotalExecs++
			if e.Exec != nil && e.Exec.Context.Reason != "" {
				reasonEntries[e.Exec.Context.Reason] = append(reasonEntries[e.Exec.Context.Reason], e)
			}
		}
		if e.Result != nil && e.Result.ExitCode != 0 {
			report.TotalErrors++
		}
	}

	report.Sessions = len(sessionKeys)

	// Compute per-command stats.
	for name, entries := range cmdEntries {
		cs := CommandStats{Name: name, Count: len(entries)}
		var durations []float64
		var heapSum float64
		var maxRSS float64

		for _, e := range entries {
			if e.Performance != nil {
				durations = append(durations, e.Performance.TotalMs)
			}
			if e.Memory != nil {
				heapSum += float64(e.Memory.HeapAllocBytes) / 1024
				rssMB := float64(e.Memory.PeakRSSBytes) / (1024 * 1024)
				if rssMB > maxRSS {
					maxRSS = rssMB
				}
			}
			if e.Result != nil && e.Result.ExitCode != 0 {
				cs.Errors++
			}
		}

		if len(durations) > 0 {
			sort.Float64s(durations)
			cs.AvgMs = avg(durations)
			cs.P50Ms = percentile(durations, 50)
			cs.P95Ms = percentile(durations, 95)
			cs.P99Ms = percentile(durations, 99)
		}
		cs.AvgHeapKB = heapSum / float64(len(entries))
		cs.MaxPeakRSSMB = maxRSS

		report.Commands = append(report.Commands, cs)
	}

	// Sort commands by count descending.
	sort.Slice(report.Commands, func(i, j int) bool {
		return report.Commands[i].Count > report.Commands[j].Count
	})

	// Compute per-reason stats.
	for reason, entries := range reasonEntries {
		rs := ExecReasonStats{Reason: reason, Count: len(entries)}
		var totalMs float64
		var totalRSS float64

		for _, e := range entries {
			if e.Exec != nil {
				totalMs += e.Exec.ChildDurationMs
				totalRSS += float64(e.Exec.ChildPeakRSSBytes) / (1024 * 1024)
			}
		}

		rs.AvgMs = totalMs / float64(len(entries))
		rs.AvgRSSMB = totalRSS / float64(len(entries))
		rs.TotalTimeS = totalMs / 1000

		report.ExecReasons = append(report.ExecReasons, rs)
	}

	// Sort reasons by count descending.
	sort.Slice(report.ExecReasons, func(i, j int) bool {
		return report.ExecReasons[i].Count > report.ExecReasons[j].Count
	})

	return report
}

func avg(vals []float64) float64 {
	sum := 0.0
	for _, v := range vals {
		sum += v
	}
	return sum / float64(len(vals))
}

func percentile(sorted []float64, pct float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(math.Ceil(pct/100*float64(len(sorted)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}
```

**Step 3: Create the test for stats**

Create `application/log_stats_test.go`:

```go
package application

import (
	"testing"

	"github.com/sosalejandro/bmad-story-runner-cli/domain"
)

func TestComputeStats_BasicCounts(t *testing.T) {
	entries := []*domain.LogEntry{
		{Type: domain.EntryTypeSessionStart, Session: domain.SessionInfo{PWD: "/a", PID: 1}},
		{Type: domain.EntryTypeCommand, Command: &domain.CommandInfo{Name: "next"}, Performance: &domain.PerformanceStats{TotalMs: 10}, Result: &domain.ResultInfo{ExitCode: 0}},
		{Type: domain.EntryTypeCommand, Command: &domain.CommandInfo{Name: "next"}, Performance: &domain.PerformanceStats{TotalMs: 20}, Result: &domain.ResultInfo{ExitCode: 0}},
		{Type: domain.EntryTypeCommand, Command: &domain.CommandInfo{Name: "status"}, Performance: &domain.PerformanceStats{TotalMs: 15}, Result: &domain.ResultInfo{ExitCode: 0}},
		{Type: domain.EntryTypeExec, Command: &domain.CommandInfo{Name: "exec"}, Exec: &domain.ExecInfo{ChildDurationMs: 1000, Context: domain.ExecContext{Reason: "json manipulation"}}, Result: &domain.ResultInfo{ExitCode: 0}},
	}

	report := ComputeStats(entries)

	if report.TotalCommands != 3 {
		t.Errorf("TotalCommands = %d, want 3", report.TotalCommands)
	}
	if report.TotalExecs != 1 {
		t.Errorf("TotalExecs = %d, want 1", report.TotalExecs)
	}
	if report.Sessions != 1 {
		t.Errorf("Sessions = %d, want 1", report.Sessions)
	}
	if len(report.ExecReasons) != 1 {
		t.Errorf("ExecReasons count = %d, want 1", len(report.ExecReasons))
	}
}

func TestComputeStats_Percentiles(t *testing.T) {
	var entries []*domain.LogEntry
	durations := []float64{5, 10, 15, 20, 25, 30, 35, 40, 45, 50}
	for _, d := range durations {
		entries = append(entries, &domain.LogEntry{
			Type:        domain.EntryTypeCommand,
			Command:     &domain.CommandInfo{Name: "next"},
			Performance: &domain.PerformanceStats{TotalMs: d},
			Result:      &domain.ResultInfo{ExitCode: 0},
		})
	}

	report := ComputeStats(entries)
	var nextStats *CommandStats
	for i := range report.Commands {
		if report.Commands[i].Name == "next" {
			nextStats = &report.Commands[i]
			break
		}
	}
	if nextStats == nil {
		t.Fatal("no stats for 'next' command")
	}
	if nextStats.AvgMs != 27.5 {
		t.Errorf("AvgMs = %f, want 27.5", nextStats.AvgMs)
	}
	if nextStats.P50Ms != 25.0 {
		t.Errorf("P50Ms = %f, want 25.0", nextStats.P50Ms)
	}
}

func TestPercentile_EdgeCases(t *testing.T) {
	if p := percentile(nil, 50); p != 0 {
		t.Errorf("nil slice: got %f, want 0", p)
	}
	if p := percentile([]float64{42}, 99); p != 42 {
		t.Errorf("single element: got %f, want 42", p)
	}
}
```

**Step 4: Run stats tests**

Run: `cd /home/as-main/Documents/projects/bmad-story-runner-cli && go test ./application/ -v -run TestComputeStats`
Expected: All PASS.

**Step 5: Create `cmd/log_show.go`**

```go
package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

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

			if entries == nil {
				fmt.Println("No log entries found.")
				return
			}

			// Filter by type.
			if entryType != "" {
				var filtered []*infrastructure.LogEntryAlias
				_ = filtered // type filtering happens below
			}

			// Filter by date range.
			if since != "" {
				sinceTime, err := time.Parse("2006-01-02", since)
				exitOnError(err)
				var filtered []*infrastructure.LogEntryAlias
				_ = filtered
				_ = sinceTime
				// Filter entries where Timestamp >= sinceTime
			}

			// Take last N entries.
			if last > 0 && len(entries) > last {
				entries = entries[len(entries)-last:]
			}

			// Render.
			for _, e := range entries {
				if entryType != "" && e.Type != entryType {
					continue
				}
				if jsonOutput {
					// Already JSONL — just print
					fmt.Println(e)
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
```

**Note:** The `printEntry` function and the actual filtering need proper implementation. This is the skeleton — the rendering function should format entries like the design doc shows. Implement `printEntry` as a helper in the same file.

Add to `cmd/log_show.go`:

```go
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
```

**Step 6: Create `cmd/log_stats.go`**

```go
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
```

**Step 7: Create `cmd/log_patterns.go`**

```go
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
```

**Step 8: Create `cmd/log_rotate.go`**

```go
package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sosalejandro/bmad-story-runner-cli/infrastructure"
)

func newLogRotateCmd() *cobra.Command {
	var global bool
	var projectPath string

	cmd := &cobra.Command{
		Use:   "rotate",
		Short: "Archive current log and start fresh",
		Run: func(cmd *cobra.Command, args []string) {
			logPath := resolveLogPath(global, projectPath)
			lw := infrastructure.NewJSONLLogWriter(logPath, globalLogPath())

			if err := lw.Rotate(logPath); err != nil {
				exitOnError(err)
			}
			fmt.Printf("Rotated %s\n", logPath)
		},
	}

	cmd.Flags().BoolVar(&global, "global", false, "Rotate global log")
	cmd.Flags().StringVar(&projectPath, "project", "", "Path to project log")

	return cmd
}
```

**Step 9: Verify the full build compiles**

Run: `cd /home/as-main/Documents/projects/bmad-story-runner-cli && go build ./...`
Expected: Clean build.

**Step 10: Run all tests**

Run: `cd /home/as-main/Documents/projects/bmad-story-runner-cli && go test ./... -v`
Expected: All PASS.

**Step 11: Commit**

```bash
git add cmd/log.go cmd/log_show.go cmd/log_stats.go cmd/log_patterns.go cmd/log_rotate.go application/log_stats.go application/log_stats_test.go
git commit -m "feat: add bmad log subcommand family

bmad log show — human-readable log viewer with filtering
bmad log stats — aggregated performance report with percentiles
bmad log patterns — exec pattern analysis for workaround identification
bmad log rotate — gzipped log archival"
```

---

### Task 8: Integration test — end-to-end smoke test

Verify the full logging flow works: command writes log, exec writes log, log show reads it.

**Files:**
- Create: `cmd/integration_test.go`

**Step 1: Write the integration test**

```go
package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIntegration_LoggingFlow(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()
	projectLog := filepath.Join(dir, "bmad-audit.jsonl")
	globalLog := filepath.Join(dir, "global", "audit.jsonl")

	// Set environment so getLogWriter finds our test paths.
	os.Setenv("BMAD_TEST_PROJECT_LOG", projectLog)
	os.Setenv("BMAD_TEST_GLOBAL_LOG", globalLog)
	defer os.Unsetenv("BMAD_TEST_PROJECT_LOG")
	defer os.Unsetenv("BMAD_TEST_GLOBAL_LOG")

	// Verify log files are created after running a command.
	// (This test validates the wiring, not individual command logic.)

	if _, err := os.Stat(projectLog); !os.IsNotExist(err) {
		t.Fatal("project log should not exist before test")
	}

	// The actual integration test would invoke the binary.
	// For now, verify the log writer creates files correctly.
	t.Log("Integration test skeleton — expand with binary invocations")
}

func TestLogPath_Resolution(t *testing.T) {
	// Test that resolveLogPath finds the correct log file.
	dir := t.TempDir()
	logFile := filepath.Join(dir, "bmad-audit.jsonl")
	os.WriteFile(logFile, []byte("{}"), 0644)

	// Change to that directory temporarily.
	orig, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(orig)

	path := resolveLogPath(false, "")
	if !strings.HasSuffix(path, "bmad-audit.jsonl") {
		t.Errorf("resolveLogPath returned %q, expected path ending in bmad-audit.jsonl", path)
	}
}
```

**Step 2: Run the test**

Run: `cd /home/as-main/Documents/projects/bmad-story-runner-cli && go test ./cmd/ -v -run TestIntegration`
Expected: PASS (skeleton test).

**Step 3: Commit**

```bash
git add cmd/integration_test.go
git commit -m "test: add integration test skeleton for logging flow"
```

---

### Task 9: Remove init.go redundant filepath.Abs (if not done in Task 0)

This is handled in Task 0. If Task 0 was implemented, skip this.

---

## Execution Order Summary

| Task | Description | Dependencies | Est. Size |
|------|-------------|--------------|-----------|
| 0 | Fix relative path resolution | None | Small |
| 1 | Domain log types | None | Medium |
| 2 | Build info ldflags | None | Small |
| 3 | Process metrics collector | Task 1 | Medium |
| 4 | LogWriter port + JSONL adapter | Task 1 | Medium |
| 5 | Logging middleware in root.go | Tasks 1-4 | Medium |
| 6 | `bmad exec` command | Tasks 1, 3, 4, 5 | Medium |
| 7 | `bmad log` subcommand family | Tasks 1, 4, 5 | Large |
| 8 | Integration test | Tasks 5-7 | Small |

Tasks 0, 1, 2 can run in parallel (no dependencies).
Tasks 3, 4 can run in parallel (both depend only on Task 1).
Task 5 depends on all of 1-4.
Tasks 6, 7 depend on Task 5.
Task 8 depends on everything.
