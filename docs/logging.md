# Logging & Telemetry Reference

`bmad` produces structured audit logs for every command invocation, tracking performance metrics, memory consumption, and I/O statistics. Logs are written in JSONL format (one JSON object per line) to two locations simultaneously.

## Log File Locations

| Log | Path | Purpose |
|-----|------|---------|
| **Project log** | `<docs-folder>/bmad-audit.jsonl` | Project-specific command history |
| **Global log** | `~/.bmad/audit.jsonl` | Cross-project pattern analysis |

The project log path is auto-detected from the progress file's `docs_folder` field. The global log directory (`~/.bmad/`) is created automatically on first use.

## Disabling Logging

```bash
bmad --no-log next ./bmad-progress.json
```

The `--no-log` global flag disables audit logging for a single invocation. Useful for scripting or when log I/O is undesirable.

## Session Identification

Each log session is identified by `pwd` + `pid`. On the first command of a new session, a `session_start` entry is written containing full environment metadata:

- **PWD**: working directory (encodes project context and worktree path)
- **PID/PPID**: process tree for tracing what spawned `bmad`
- **Terminal**: parent process name (e.g., `claude-code`, `bash`, `zsh`)
- **Build info**: `bmad_version`, `bmad_commit`, `go_version`
- **Host info**: `hostname`, `os`, `arch`, `user`

## Entry Types

### `session_start`

Written once per session. Contains only the `session` block — no command or performance data.

### `command`

Written for every `bmad` command (init, next, status, set-status, etc.). Contains:

- **`command`**: name, args, raw string, parsed flags
- **`result`**: exit code, stdout/stderr byte counts, error message and sentinel error type
- **`performance`**: total duration, I/O read/write time, parse time, logic time, wall clock nanoseconds
- **`memory`**: heap allocation, total allocation, GC count and pause times, goroutine count, stack usage, peak RSS, malloc/free counts
- **`io`**: files read/written, bytes read/written, stories processed count

### `exec`

Written for `bmad exec` commands. Contains everything from `command` entries plus:

- **`exec.child_command`**: the wrapped command name
- **`exec.child_args`**: argument list
- **`exec.child_exit_code`**: child process exit code
- **`exec.child_duration_ms`**: child wall-clock time
- **`exec.child_stdout_bytes`** / **`child_stderr_bytes`**: output sizes
- **`exec.child_peak_rss_bytes`**: child peak memory (from `Rusage.Maxrss`)
- **`exec.child_user_time_ms`** / **`child_sys_time_ms`**: child CPU time (from `Rusage`)
- **`exec.context.previous_bmad_command`**: auto-detected from last log entry
- **`exec.context.reason`**: user-supplied `--reason` flag

---

## Commands

### `bmad exec`

Wrap an arbitrary shell command, logging it as an escape-hatch operation with full telemetry.

```bash
bmad exec [--reason <why>] [--context <previous-bmad-cmd>] -- <command> [args...]
```

**Behavior:**
1. Logs the full exec entry with reason and context metadata
2. Runs the child command, passing through stdin/stdout/stderr transparently
3. Captures child process metrics (CPU time, peak RSS) via `syscall.Rusage`
4. Exits with the child's exit code

**Flags:**

| Flag | Default | Description |
|------|---------|-------------|
| `--reason` | `""` | Why this command is being run (for pattern analysis) |
| `--context` | auto-detected | Which `bmad` command preceded this exec call |

**Examples:**

```bash
# Wrap a Python script that manipulates progress data
bmad exec --reason "filter stories by status" -- python3 -c "import json; ..."

# Wrap a jq query
bmad exec --reason "extract story titles" -- jq '.stories[].title' bmad-progress.json

# Pipe bmad output into a wrapped command
bmad next docs/bmad-progress.json | bmad exec --reason "parse story path" -- cut -d/ -f2
```

The `--reason` flag is critical for pattern analysis. When you later run `bmad log patterns`, reasons are grouped and ranked to identify which operations should become native CLI commands.

---

### `bmad log show`

Human-readable view of log entries.

```bash
bmad log show [flags]
```

**Flags:**

| Flag | Default | Description |
|------|---------|-------------|
| `--last` | `20` | Show the N most recent entries |
| `--type` | all | Filter by entry type: `command`, `exec`, `session_start` |
| `--global` | `false` | Read from global log instead of project log |
| `--project` | auto-detected | Path to project log (overrides auto-detection) |
| `--since` | none | Show entries after this date (YYYY-MM-DD) |
| `--until` | none | Show entries before this date (YYYY-MM-DD) |
| `--json` | `false` | Output raw JSONL instead of human-readable format |

**Human-readable output example:**

```
[2026-03-08 14:32:01] COMMAND next
  args:  docs/bmad-progress.json --group 1 --filter-group
  exit:  0
  time:  12.3ms (io_read: 4.1ms, parse: 3.0ms, logic: 5.2ms)
  mem:   heap=240KB peak_rss=14MB gc=2 (84us)
  io:    read 1 file (2KB), 12 stories

[2026-03-08 14:33:05] EXEC python3 -c "import json; ..."
  reason:   json manipulation
  context:  after 'next'
  exit:     0
  bmad:     1.2ms
  child:    1247ms (user: 980ms, sys: 45ms, rss: 27MB)
```

---

### `bmad log stats`

Aggregated performance report.

```bash
bmad log stats [flags]
```

**Flags:**

| Flag | Default | Description |
|------|---------|-------------|
| `--global` | `false` | Aggregate from global log |
| `--since` | 7 days ago | Start of reporting period |
| `--until` | now | End of reporting period |

**Output example:**

```
Command Performance Summary (2026-03-01 to 2026-03-08)
------------------------------------------------------
Command         Count   Avg(ms)  P50(ms)  P95(ms)  P99(ms)  Avg Heap(KB)  Peak RSS(MB)  Errors
next              47     12.3     11.0     18.1     22.5     240           14            0
set-status        38      8.7      8.0     14.2     16.1     220           12            0
set-complete      12      9.1      8.5     15.8     15.8     230           13            0
status            23     15.4     14.0     22.0     28.3     310           16            0
init               3     45.2     42.0     58.1     58.1     580           22            0
exec              14    842.0    780.0   1580.0   2100.0     ---           ---           2

Total commands: 137
Total exec calls: 14
Total errors: 2
Avg GC pauses: 42us

Top exec reasons:
  "json manipulation"         6 calls, avg 1247ms, avg child RSS 27MB
  "filter stories"            4 calls, avg  890ms, avg child RSS 18MB
  "parse output"              4 calls, avg  340ms, avg child RSS  8MB

Sessions: 8 unique (by pwd+pid)
```

---

### `bmad log patterns`

Exec pattern analysis — identifies recurring workarounds that should become native CLI commands.

```bash
bmad log patterns [flags]
```

**Flags:**

| Flag | Default | Description |
|------|---------|-------------|
| `--global` | `false` | Analyze global log |
| `--min-count` | `2` | Minimum occurrences to show |
| `--sort` | `count` | Sort by: `count`, `duration`, `memory` |

**Output example:**

```
Exec Pattern Analysis (14 total exec calls)
-----------------------------------------------------
Reason                    Count  Avg(ms)  Avg RSS(MB)  Total Time  Suggestion
"json manipulation"         6    1247     27           7.48s       -> Consider: bmad query <json> <jq-expr>
"filter stories by status"  4     890     18           3.56s       -> Consider: bmad status --filter <status>
"parse story path"          4     340      8           1.36s       -> Consider: bmad next --format <template>

Top child commands:
  python3     8 calls (57%), avg 1100ms
  jq          4 calls (29%), avg  340ms
  cut         2 calls (14%), avg   12ms

Estimated savings if native:
  14 exec calls x avg 842ms = 11.8s total
  Native equivalent estimate: ~0.17s (14 x 12ms avg)
  Potential savings: 11.6s (98.5%)
```

---

### `bmad log rotate`

Archive current log file.

```bash
bmad log rotate [flags]
```

**Flags:**

| Flag | Default | Description |
|------|---------|-------------|
| `--global` | `false` | Rotate global log |
| `--project` | auto-detected | Path to project log |

Archives to `audit-YYYY-MM-DD.jsonl.gz` in the same directory and starts a fresh log file.

**Auto-rotation:** Set `--max-log-size` on any `bmad` command to enable automatic rotation when the log exceeds the specified size:

```bash
bmad --max-log-size 10MB next ./bmad-progress.json
```

---

## Querying Logs Directly

Since logs are JSONL, you can query them with standard tools:

```bash
# All exec reasons
jq -r 'select(.type=="exec") | .exec.context.reason' ~/.bmad/audit.jsonl | sort | uniq -c | sort -rn

# Slowest commands
jq -r 'select(.type=="command") | "\(.performance.total_ms)ms \(.command.name)"' bmad-audit.jsonl | sort -rn | head

# Memory hogs
jq -r 'select(.memory.peak_rss_bytes > 20000000) | "\(.memory.peak_rss_bytes / 1048576)MB \(.command.name)"' bmad-audit.jsonl

# Commands with errors
jq 'select(.result.exit_code != 0)' bmad-audit.jsonl

# Session timeline
jq -r 'select(.type=="session_start") | "\(.timestamp) \(.session.pwd) pid=\(.session.pid)"' bmad-audit.jsonl
```

## Global Flags Reference

| Flag | Default | Scope | Description |
|------|---------|-------|-------------|
| `--no-log` | `false` | All commands | Disable audit logging for this invocation |
| `--max-log-size` | `0` (disabled) | All commands | Auto-rotate when log exceeds this size |
| `--log-project-path` | auto-detected | All commands | Override project log file location |
