# Logging, Telemetry & Exec Wrapper Design

## Problem

Claude Code frequently reaches for Python/bash one-liners to manipulate data that `bmad` should handle natively. There is no mechanism to:

1. Track which `bmad` commands are executed and how they perform
2. Capture the ad-hoc workarounds Claude uses between `bmad` calls
3. Identify patterns that should become native CLI commands

## Solution

Three additions to the `bmad` CLI:

1. **Structured audit logging** on every command invocation (dual-write to project + global log)
2. **`bmad exec`** command that wraps arbitrary shell commands, logging them as escape-hatch operations
3. **`bmad log` subcommand family** for querying, analyzing, and rotating logs

## Log Entry Schema (v1.0.0)

### Entry Types

All entries share a `type` field: `session_start`, `command`, or `exec`.

### Session Start Entry

Written on the first command of a new session (detected by PWD + PID not matching the last entry):

```json
{
  "type": "session_start",
  "version": "1.0.0",
  "timestamp": "2026-03-08T14:30:00.000000Z",
  "session": {
    "pwd": "/home/as-main/Documents/projects/my-app",
    "terminal": "claude-code",
    "pid": 12345,
    "ppid": 12340,
    "user": "as-main",
    "hostname": "dev-workstation",
    "os": "linux",
    "arch": "amd64",
    "go_version": "go1.25.7",
    "bmad_version": "0.4.0",
    "bmad_commit": "a3f8c2e"
  }
}
```

### Command Entry

Written for every `bmad` command (init, next, status, set-status, etc.):

```json
{
  "type": "command",
  "version": "1.0.0",
  "timestamp": "2026-03-08T14:32:01.123456Z",
  "session": {
    "pwd": "/home/as-main/Documents/projects/my-app",
    "terminal": "claude-code",
    "pid": 12345,
    "ppid": 12340,
    "user": "as-main",
    "hostname": "dev-workstation",
    "os": "linux",
    "arch": "amd64",
    "go_version": "go1.25.7",
    "bmad_version": "0.4.0",
    "bmad_commit": "a3f8c2e"
  },
  "command": {
    "name": "next",
    "args": ["docs/bmad-progress.json", "--group", "1", "--filter-group"],
    "raw": "bmad next docs/bmad-progress.json --group 1 --filter-group",
    "flags": {
      "group": 1,
      "filter-group": true
    }
  },
  "result": {
    "exit_code": 0,
    "stdout_bytes": 48,
    "stderr_bytes": 0,
    "error": null,
    "error_type": null
  },
  "performance": {
    "total_ms": 12.34,
    "io_read_ms": 4.12,
    "io_write_ms": 0,
    "parse_ms": 3.01,
    "logic_ms": 5.21,
    "wall_clock_ns": 12340000
  },
  "memory": {
    "heap_alloc_bytes": 245760,
    "heap_sys_bytes": 3538944,
    "total_alloc_bytes": 512000,
    "num_gc": 2,
    "gc_pause_total_ns": 84000,
    "gc_pause_last_ns": 42000,
    "goroutines": 3,
    "stack_inuse_bytes": 327680,
    "peak_rss_bytes": 14680064,
    "mallocs": 4200,
    "frees": 3800
  },
  "io": {
    "files_read": 1,
    "files_written": 0,
    "bytes_read": 2048,
    "bytes_written": 0,
    "stories_processed": 12
  },
  "exec": null
}
```

### Exec Entry

Written for `bmad exec` commands — wraps an arbitrary child process:

```json
{
  "type": "exec",
  "version": "1.0.0",
  "timestamp": "2026-03-08T14:33:05.000000Z",
  "session": { "..." : "same as command entry" },
  "command": {
    "name": "exec",
    "args": ["--reason", "json manipulation", "--", "python3", "-c", "import json; ..."],
    "raw": "bmad exec --reason 'json manipulation' -- python3 -c 'import json; ...'"
  },
  "result": {
    "exit_code": 0,
    "stdout_bytes": 0,
    "stderr_bytes": 0,
    "error": null,
    "error_type": null
  },
  "performance": {
    "total_ms": 1260.5,
    "io_read_ms": 0,
    "io_write_ms": 0,
    "parse_ms": 0,
    "logic_ms": 1.2,
    "wall_clock_ns": 1260500000
  },
  "memory": {
    "heap_alloc_bytes": 180000,
    "heap_sys_bytes": 3538944,
    "total_alloc_bytes": 200000,
    "num_gc": 0,
    "gc_pause_total_ns": 0,
    "gc_pause_last_ns": 0,
    "goroutines": 3,
    "stack_inuse_bytes": 327680,
    "peak_rss_bytes": 10485760,
    "mallocs": 1200,
    "frees": 1100
  },
  "io": {
    "files_read": 0,
    "files_written": 0,
    "bytes_read": 0,
    "bytes_written": 0,
    "stories_processed": 0
  },
  "exec": {
    "child_command": "python3",
    "child_args": ["-c", "import json; ..."],
    "child_exit_code": 0,
    "child_duration_ms": 1247.5,
    "child_stdout_bytes": 512,
    "child_stderr_bytes": 0,
    "child_peak_rss_bytes": 28311552,
    "child_user_time_ms": 980.2,
    "child_sys_time_ms": 45.1,
    "context": {
      "previous_bmad_command": "next",
      "reason": "json manipulation"
    }
  }
}
```

## Log File Locations

| Log | Path | Purpose |
|-----|------|---------|
| Project log | `<docs-folder>/bmad-audit.jsonl` | Project-specific history |
| Global log | `~/.bmad/audit.jsonl` | Cross-project pattern analysis |

Both are written simultaneously on every command invocation.

### Rotation

- No automatic rotation by default (CLI tools don't generate massive volume)
- `bmad log rotate` archives current log to `audit-YYYY-MM-DD.jsonl.gz`
- Optional `--max-log-size` global flag on root command for auto-rotation threshold

### Session Detection

A `session_start` entry is written when PWD + PID does not match the last entry in the project log. This identifies which application/terminal started the session — PWD encodes both the project context and the worktree path (if using git worktrees for parallel agents).

## `bmad exec` Command

### Usage

```bash
bmad exec [--reason <why>] [--context <previous-bmad-cmd>] -- <command> [args...]
```

### Behavior

1. Logs the exec entry with reason and context metadata
2. Runs the child command, passing through stdin/stdout/stderr transparently
3. Captures child process metrics via `os.ProcessState.SysUsage()` → `syscall.Rusage`
4. Writes the complete log entry with both `bmad exec` overhead and child metrics
5. Exits with the child's exit code (transparent wrapper)

### Context Auto-Population

If `--context` is not provided, `bmad exec` reads the last entry from the project log and uses its command name as the context. This links the exec call to the preceding `bmad` command automatically.

### Examples

```bash
# Claude manipulating JSON that bmad should handle
bmad exec --reason "filter stories by status" -- python3 -c "import json; ..."

# Claude doing a jq query on progress data
bmad exec --reason "extract story titles" -- jq '.stories[].title' bmad-progress.json

# Piping bmad output into something
bmad next docs/bmad-progress.json | bmad exec --reason "parse story path" -- cut -d/ -f2
```

## `bmad log` Subcommand Family

### `bmad log show`

Human-readable view of log entries.

```bash
bmad log show [--last N] [--type command|exec|session_start] [--project <path>]
bmad log show --global
bmad log show --since 2026-03-01 --until 2026-03-08
bmad log show --type exec   # only workaround patterns
```

### `bmad log stats`

Performance report with aggregated metrics.

```bash
bmad log stats [--global] [--since DATE] [--until DATE]
```

Output:
```
Command Performance Summary (last 7 days)
------------------------------------------------------
Command         Count   Avg(ms)  P95(ms)  Avg Heap(KB)  Errors
next              47     12.3     18.1     240           0
set-status        38      8.7     14.2     220           0
set-complete      12      9.1     15.8     230           0
status            23     15.4     22.0     310           0
exec              14    842.0   1580.0     ---           2

Top exec reasons:
  "json manipulation"         6 calls, avg 1247ms
  "filter stories"            4 calls, avg  890ms
  "parse output"              4 calls, avg  340ms
```

### `bmad log patterns`

Exec pattern analysis — identifies recurring workarounds.

```bash
bmad log patterns [--global] [--min-count N]
```

Output:
```
Exec Pattern Analysis
-----------------------------------------------------
Reason                    Count  Avg(ms)  Suggestion
"json manipulation"         6    1247ms   -> Consider: bmad query <json> <jq-expr>
"filter stories by status"  4     890ms   -> Consider: bmad status --filter <status>
"parse story path"          4     340ms   -> Consider: bmad next --format <template>
```

### `bmad log rotate`

Archive current log.

```bash
bmad log rotate [--global] [--project <path>]
```

Archives to `audit-YYYY-MM-DD.jsonl.gz` and starts a fresh log file.

## Architecture

### New Files

| File | Layer | Purpose |
|------|-------|---------|
| `domain/log.go` | Domain | `LogEntry`, `SessionInfo`, `CommandInfo`, `ResultInfo`, `PerformanceStats`, `MemoryStats`, `IOStats`, `ExecInfo` types |
| `domain/log_config.go` | Domain | `LogConfig` with paths, rotation settings |
| `application/ports/log_writer.go` | Ports | `LogWriter` interface: `WriteEntry()`, `ReadEntries()`, `Rotate()`, `LastEntry()` |
| `application/log_stats.go` | Application | Use case: aggregate stats, percentiles, error rates |
| `application/log_patterns.go` | Application | Use case: group exec entries by reason, rank by frequency |
| `application/exec_command.go` | Application | Use case: run child process, capture `Rusage`, build log entry |
| `infrastructure/jsonl_log_writer.go` | Infrastructure | Dual-write adapter: project + global JSONL, rotation, reading |
| `infrastructure/process_metrics.go` | Infrastructure | `runtime.MemStats`, `/proc/self/status` RSS, child `Rusage` collection |
| `cmd/exec.go` | CLI | `bmad exec` command |
| `cmd/log.go` | CLI | `bmad log` parent command |
| `cmd/log_show.go` | CLI | `bmad log show` command |
| `cmd/log_stats.go` | CLI | `bmad log stats` command |
| `cmd/log_patterns.go` | CLI | `bmad log patterns` command |
| `cmd/log_rotate.go` | CLI | `bmad log rotate` command |

### Modified Files

| File | Change |
|------|--------|
| `cmd/root.go` | Add `log` and `exec` subcommands, initialize `LogWriter`, add `--max-log-size` and `--no-log` global flags |
| `cmd/bmad/main.go` | Ensure `~/.bmad/` directory exists, pass build info via `ldflags` |
| `go.mod` | No new dependencies (all stdlib + existing zap) |

### Instrumentation: Middleware Wrapper

Instead of modifying every command, a single middleware in `cmd/root.go`:

```go
func withLogging(lw LogWriter, metrics *ProcessMetrics, fn func(cmd *cobra.Command, args []string) error) func(cmd *cobra.Command, args []string) {
    return func(cmd *cobra.Command, args []string) {
        entry := metrics.StartEntry(cmd, args)  // captures start time, baseline memstats
        err := fn(cmd, args)
        entry.Finish(err)                        // captures end time, delta memstats, exit code, RSS
        lw.WriteEntry(entry)                     // dual-write to project + global
    }
}
```

Each command registration becomes:

```go
cmd.RunE = withLogging(logWriter, metrics, originalRunE)
```

Existing command code remains untouched — logging is a cross-cutting concern.

### Build Info Injection

Version and commit hash injected at build time via `ldflags`:

```go
// cmd/version.go
var (
    Version   = "dev"
    CommitSHA = "unknown"
)
```

```bash
go build -ldflags "-X cmd.Version=0.4.0 -X cmd.CommitSHA=$(git rev-parse --short HEAD)" ./cmd/bmad
```

### Memory Collection

```go
// infrastructure/process_metrics.go

func collectMemoryStats() MemoryStats {
    var m runtime.MemStats
    runtime.ReadMemStats(&m)
    return MemoryStats{
        HeapAllocBytes:   m.HeapAlloc,
        HeapSysBytes:     m.HeapSys,
        TotalAllocBytes:  m.TotalAlloc,
        NumGC:            m.NumGC,
        GCPauseTotalNs:   m.PauseTotalNs,
        GCPauseLastNs:    m.PauseNs[(m.NumGC+255)%256],
        Goroutines:       runtime.NumGoroutine(),
        StackInuseBytes:  m.StackInuse,
        PeakRSSBytes:     readPeakRSS(),  // /proc/self/status VmHWM
        Mallocs:          m.Mallocs,
        Frees:            m.Frees,
    }
}

func readPeakRSS() uint64 {
    // Parse /proc/self/status for VmHWM line (Linux only)
    // Returns 0 on non-Linux platforms
}
```

### Child Process Metrics (for `bmad exec`)

```go
func collectChildMetrics(state *os.ProcessState) ExecMetrics {
    rusage := state.SysUsage().(*syscall.Rusage)
    return ExecMetrics{
        ChildUserTimeMs:    float64(rusage.Utime.Sec)*1000 + float64(rusage.Utime.Usec)/1000,
        ChildSysTimeMs:     float64(rusage.Stime.Sec)*1000 + float64(rusage.Stime.Usec)/1000,
        ChildPeakRSSBytes:  uint64(rusage.Maxrss) * 1024, // Maxrss is in KB on Linux
    }
}
```

### I/O Tracking

The `IOStats` fields (`files_read`, `files_written`, `bytes_read`, `bytes_written`, `stories_processed`) are populated by the use cases themselves. The middleware provides an `IOTracker` that use cases increment:

```go
type IOTracker struct {
    FilesRead        int
    FilesWritten     int
    BytesRead        int64
    BytesWritten     int64
    StoriesProcessed int
}
```

Use cases that already have a `*zap.Logger` will also receive an `*IOTracker` via the command setup. The tracker is read by the middleware after the use case returns.

### Performance Phase Tracking

The `PerformanceStats` breakdown (`io_read_ms`, `io_write_ms`, `parse_ms`, `logic_ms`) uses a simple phase timer that the infrastructure adapters call:

```go
type PhaseTimer struct {
    start  time.Time
    phases map[string]time.Duration
    current string
}

func (t *PhaseTimer) StartPhase(name string) { ... }
func (t *PhaseTimer) EndPhase()              { ... }
func (t *PhaseTimer) Results() map[string]float64 { ... }
```

Infrastructure adapters (`JSONProgressStore.Load()`, `.Save()`) call `StartPhase("io_read")` / `EndPhase()` around their I/O operations. Parsing phases are tracked similarly.

## Global Flags

Added to root command:

| Flag | Default | Purpose |
|------|---------|---------|
| `--no-log` | `false` | Disable audit logging for this invocation |
| `--max-log-size` | `0` (disabled) | Auto-rotate when log exceeds this size (e.g., `10MB`) |
| `--log-project-path` | auto-detected | Override project log location |

Project log path is auto-detected from the `docs_folder` field in `bmad-progress.json` when the first argument is a progress file path. For commands that take a docs folder (like `init`, `scan`), it uses that path directly.

## Testing Strategy

- **Unit tests**: Domain types, stats aggregation, pattern grouping, phase timer
- **Integration tests**: JSONL writer dual-write, rotation, session detection
- **Table-driven tests**: Log entry serialization/deserialization roundtrips
- **Exec tests**: Child process metric collection with known-duration commands

## Path Resolution Fix

All commands should resolve relative paths to absolute before use. Currently, passing `docs/features/.../bmad-progress.json` fails if the working directory doesn't match expectations. Fix: call `filepath.Abs()` on all path arguments in the command layer before passing to use cases.

## Future: Extract to Standalone Module

The logging/telemetry code has zero dependencies on `bmad` domain types. After validating the API against this real use case, extract to a standalone Go module (e.g., `github.com/sosalejandro/cli-audit-log`) so it can be reused across other CLI tools. The design already separates concerns cleanly — extraction will be mechanical.

## No New Dependencies

All functionality uses:
- `runtime` (MemStats, NumGoroutine)
- `syscall` (Rusage for child process metrics)
- `os` (process info, file I/O)
- `encoding/json` (JSONL serialization)
- `compress/gzip` (log rotation)
- `sort`, `math` (stats aggregation, percentiles)
- Existing `go.uber.org/zap` (structured logging — unchanged)
