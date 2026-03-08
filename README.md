# bmad — BMAD Story Runner CLI

A Go CLI tool that replaces ad-hoc Python/bash scripts in the [BMAD Story Runner skill](https://github.com/sosalejandro/bmad-story-runner-cli). It manages `bmad-progress.json`, story status transitions, QA gate checks, and reconciliation — all from a single binary.

## Installation

```bash
go install github.com/sosalejandro/bmad-story-runner-cli/cmd/bmad@latest
```

Requires Go 1.24+. The binary is installed as `bmad`. Add `$(go env GOPATH)/bin` to your `$PATH` if not already present.

## Architecture

Hexagonal architecture with four layers:

```
domain/           Pure types, sentinel errors — no I/O
application/      Use cases wired through port interfaces
  ports/          ProgressStore, StoryScanner, GateReader interfaces
infrastructure/   Concrete adapters (filesystem, JSON, YAML, Markdown)
cmd/              Cobra CLI — wires use cases, handles output
```

## Commands

### `bmad init <docs-folder>`

Scan a docs folder recursively for story `.md` files and create `bmad-progress.json` at the docs folder root.

```bash
bmad init ./docs/features/payment-system/stories
```

- Skips `README.md` and `bmad-progress.json`
- Maps free-form `**Status:**` values in story files to canonical statuses
- Stories that appear complete are flagged with `ci_passed: false` (unverified)

---

### `bmad status <progress-json>`

Show a progress summary table.

```bash
bmad status ./docs/.../bmad-progress.json
bmad status ./docs/.../bmad-progress.json --group 2 --filter-group
```

Output:
```
BMAD Progress -- /path/to/docs
--------------------------------------------------
Group 1: 3 / 5 complete
Group 2: 1 / 4 complete

  in-progress:  1
  qa-review:    2
  pending:      3

  Blocked: 4.4 (waiting on: [3.2])

Up next: 3.2 -- Payment webhook handler
```

Flags:
- `--group <n>` + `--filter-group` — restrict output to a specific parallel group

---

### `bmad set-status <progress-json> <story-id> <status>`

Update a single story's status and `last_updated`.

```bash
bmad set-status ./bmad-progress.json 2.8.checkout-session-verification in-progress
bmad set-status ./bmad-progress.json 2.8.checkout-session-verification qa-review

# Short prefix also works — "2.8" resolves to "2.8.checkout-session-verification"
bmad set-status ./bmad-progress.json 2.8 in-progress
```

Valid statuses: `pending` | `in-progress` | `qa-review` | `complete` | `blocked`

Story IDs support prefix matching: `2.8` will match `2.8.checkout-session-verification` if no exact match is found. If multiple stories share the same prefix, the first exact match wins.

Exits non-zero with a structured error if the story ID is not found or the status is invalid.

---

### `bmad set-complete <progress-json> <story-id>`

Atomically mark a story complete after CI passes. Sets `status=complete`, `ci_passed=true`, and `completed_at=<now>` in a single write.

```bash
bmad set-complete ./bmad-progress.json 2.8.checkout-session-verification
```

> Only call this after `task ci` passes. Use `set-status` if you need to set status independently.

---

### `bmad bulk-complete <progress-json> <story-id> [story-id ...]`

Mark multiple stories complete in a single JSON write. Use when CI has passed for a batch of stories at once — e.g. after parallel QA agents have reviewed and approved a group.

```bash
bmad bulk-complete ./bmad-progress.json \
  3.2.organization-members-repository \
  4.4.feature-access-evaluator \
  5.7.stripe-price-synchronization \
  6.1.invoices-repository-service \
  6.6.dunning-management
```

Sets `status=complete`, `ci_passed=true`, and `completed_at=now` for all listed stories in one atomic write. Stories not found in the progress file are reported as warnings; if some were completed and some not found, exits with a warning rather than non-zero so callers can continue.

Use `bmad set-complete` when completing a single story. Use `bmad bulk-complete` when completing a batch after parallel QA or a shared CI run.

---

### `bmad add-concerns <progress-json> <story-id> <concerns-json>`

Append QA concerns to a story's `qa_concerns` array.

```bash
bmad add-concerns ./bmad-progress.json 3.2.webhook-handler \
  '[{"severity":"high","note":"missing error boundary on webhook retry"}]'
```

`concerns-json` must be a valid JSON array of objects with `severity` and `note` fields.

---

### `bmad next <progress-json>`

Print the absolute file path of the next eligible story — `status=pending` with all blockers complete — sorted by numeric filename prefix.

```bash
bmad next ./bmad-progress.json
bmad next ./bmad-progress.json --group 1 --filter-group
```

Flags:
- `--group <n>` + `--filter-group` — restrict to a specific parallel group

Exit codes:
- `0` — path printed to stdout
- `2` — no eligible story found (prints `NONE` to stderr)
- `1` — error

---

### `bmad mark-story-file <story-file> <status>`

Patch the `**Status:**` line in a story `.md` file. Inserts the line after the first heading if not already present.

```bash
bmad mark-story-file ./stories/2.8.checkout-session-verification.md Done
```

---

### `bmad scan <docs-folder>`

List all story files with task and acceptance criteria (AC) completion counts. Replaces the bash loop that used `grep -c '\[x\]'`.

```bash
bmad scan ./docs/features/payment-system/stories
```

Output:
```
STORY        ACs    TASKS        TITLE
------------------------------------------------------------
2.8          6      33/33        Checkout Session Verification
3.2          7      28/31        Payment Webhook Handler
```

---

### `bmad assign-groups <progress-json> <n>`

Distribute all stories across N parallel groups by epic/module. Run this once after `bmad init` when setting up a multi-agent session.

```bash
bmad assign-groups ./docs/stories/bmad-progress.json 3

# Re-balance after adding stories (overwrites existing assignments)
bmad assign-groups ./docs/stories/bmad-progress.json 3 --force
```

Grouping strategy:
- Stories that share the same top-level subdirectory (e.g. `epic-1/`) are always kept in the same group — agents working the same module conflict with each other.
- For flat story layouts (no subdirectories), groups by the first numeric segment of the story ID (e.g. `"1"` from `"1.2.some-story"`).
- Groups are balanced by story count using a greedy bin-packing algorithm, so each agent receives roughly the same amount of work.

Output:
```
Assigned 15 stories across 3 groups:

  Group 1: 5 stories  [epic-1]
  Group 2: 6 stories  [epic-2, epic-3]
  Group 3: 4 stories  [epic-4]
```

Flags:
- `--force` — overwrite existing group assignments (for re-balancing)

Exits non-zero if stories already have group assignments and `--force` is not set.

---

### `bmad assign-session <progress-json> <group> <session-id>`

Assign a session identifier to all unassigned stories in a parallel group. Used when a new agent joins a parallel session.

```bash
bmad assign-session ./bmad-progress.json 2 session-abc123
```

---

### `bmad qa-pending <docs-folder>`

List story files that still contain the placeholder text `To be populated` in their QA section. Use before dispatching QA agents to know which stories need review.

```bash
bmad qa-pending ./docs/features/payment-system/stories
```

Output:
```
3 story file(s) with pending QA:
  /abs/path/3.2.webhook-handler.md
  /abs/path/4.4.stripe-integration.md
  /abs/path/5.7.refund-flow.md
```

---

### `bmad write-gate <progress-json> <story-id> <PASS|FAIL|CONCERNS>`

Write (or overwrite) the QA gate YAML file for a story at `<docs_folder>/qa/gates/<story-id>.yml`. Creates the `qa/gates/` directory if it does not exist.

```bash
# Story passed QA
bmad write-gate ./bmad-progress.json 2.9.subscription-management-endpoints PASS

# Short ID also works (prefix matching)
bmad write-gate ./bmad-progress.json 2.9 PASS

# Story has concerns — supply them as a JSON array
bmad write-gate ./bmad-progress.json 3.2 CONCERNS \
  '[{"severity":"high","note":"missing retry on webhook"},{"severity":"mild","note":"no structured logging"}]'

# Or use the --concerns flag
bmad write-gate ./bmad-progress.json 3.2 FAIL \
  --concerns '[{"severity":"high","note":"auth check bypassed"}]'
```

The story ID supports prefix matching: `2.9` resolves to `2.9.subscription-management-endpoints`.

---

### `bmad gate-check <progress-json>`

Read all gate YAML files from `<docs_folder>/qa/gates/*.yml` and print a `PASS/FAIL/CONCERNS` table. Exits non-zero if any story has `FAIL` or `CONCERNS`.

```bash
bmad gate-check ./bmad-progress.json
```

Output:
```
STORY                RESULT
-----------------------------------
3.2                  PASS
4.4                  CONCERNS
5.7                  PASS

Gate check FAILED: one or more stories have FAIL or CONCERNS
```

Gate file format (`qa/gates/<story-id>.yml`):
```yaml
gate: PASS   # PASS | FAIL | CONCERNS
concerns:
  - severity: high
    note: "Missing retry logic on webhook handler"
```

---

### `bmad reconcile <progress-json>`

Read all gate files and update `bmad-progress.json` in one pass:
- `PASS` → `status=complete`, `ci_passed=true`, `completed_at=now`
- `FAIL` / `CONCERNS` → keep `qa-review`, append concerns to `qa_concerns`

Only processes stories currently at `qa-review` status.

```bash
bmad reconcile ./bmad-progress.json
```

Output:
```
Completed (2): [3.2 5.7]
Still blocked (1): [4.4]
```

---

## Progress File Schema

`bmad-progress.json` is co-located in the docs folder root:

```json
{
  "version": 1,
  "docs_folder": "/abs/path/to/docs/stories",
  "last_updated": "2026-03-06T14:00:00Z",
  "stories": [
    {
      "id": "2.8.checkout-session-verification",
      "file": "2.8.checkout-session-verification.md",
      "title": "Checkout Session Verification",
      "status": "complete",
      "parallel_group": 1,
      "assigned_session": "session-abc123",
      "blockers": [],
      "qa_concerns": [],
      "ci_passed": true,
      "completed_at": "2026-03-06T14:00:00Z"
    }
  ]
}
```

Valid `status` values: `pending` → `in-progress` → `qa-review` → `complete` | `blocked`

## Typical Workflow

### Sequential (single agent)

```bash
# 1. Initialize
bmad init ./docs/stories

# 2. Find and claim the first story
bmad next ./docs/stories/bmad-progress.json
bmad set-status ./bmad-progress.json 2.8.story in-progress

# 3. After dev completes, move to QA
bmad set-status ./bmad-progress.json 2.8.story qa-review

# 4. Write QA gate result (replaces cat/echo for gate files)
bmad write-gate ./bmad-progress.json 2.8.story PASS
# or with concerns:
bmad write-gate ./bmad-progress.json 2.8.story CONCERNS \
  '[{"severity":"high","note":"missing retry logic"}]'
bmad add-concerns ./bmad-progress.json 2.8.story '[{"severity":"high","note":"..."}]'

# 5. After CI passes, mark complete and patch the story file
bmad set-complete ./bmad-progress.json 2.8.story
bmad mark-story-file ./docs/stories/2.8.story.md Done

# 6. Check overall progress
bmad status ./bmad-progress.json
```

### Parallel (multiple agents)

```bash
# ── Setup (done once by the primary agent) ────────────────────────────
# 1. Initialize and distribute stories across 3 groups
bmad init ./docs/stories
bmad assign-groups ./docs/stories/bmad-progress.json 3

# 2. Each agent claims a group and gets its first story
bmad assign-session ./bmad-progress.json 1 session-agent-1   # agent 1
bmad assign-session ./bmad-progress.json 2 session-agent-2   # agent 2
bmad assign-session ./bmad-progress.json 3 session-agent-3   # agent 3

# ── Each agent runs independently ─────────────────────────────────────
bmad next ./bmad-progress.json --group 1 --filter-group
bmad set-status ./bmad-progress.json 1.1.story in-progress
# ... dev agent ... qa agent ...
bmad set-complete ./bmad-progress.json 1.1.story
bmad mark-story-file ./docs/stories/1.1.story.md Done

# ── After parallel QA batch: reconcile all at once ────────────────────
bmad gate-check ./bmad-progress.json
bmad reconcile ./bmad-progress.json

# Bulk-complete when CI has covered a batch
bmad bulk-complete ./bmad-progress.json \
  3.2.story-a 4.4.story-b 5.7.story-c

# Overall progress across all groups
bmad status ./bmad-progress.json
```

### `bmad exec [--reason <why>] -- <command> [args...]`

Wrap an arbitrary shell command with full audit logging and telemetry. Use this when the CLI doesn't have a native command for what you need — it captures the pattern so it can be built into the CLI later.

```bash
# Wrap a Python script that manipulates progress data
bmad exec --reason "filter stories by status" -- python3 -c "import json; ..."

# Wrap a jq query
bmad exec --reason "extract story titles" -- jq '.stories[].title' bmad-progress.json
```

Stdin/stdout/stderr pass through transparently. Exit code matches the child process. Child process CPU time, peak RSS, and duration are captured in the log.

Flags:
- `--reason <why>` — why this command is being run (critical for pattern analysis)
- `--context <cmd>` — which `bmad` command preceded this (auto-detected from last log entry)

---

### `bmad log show [--last N] [--type TYPE] [--global]`

Human-readable view of recent audit log entries.

```bash
bmad log show                          # last 20 entries from project log
bmad log show --last 50 --type exec    # last 50 exec entries
bmad log show --global --since 2026-03-01
```

Flags:
- `--last <n>` — number of entries (default: 20)
- `--type <command|exec|session_start>` — filter by entry type
- `--global` — read from global log (`~/.bmad/audit.jsonl`) instead of project log
- `--project <path>` — override project log path
- `--since` / `--until` — date range filter (YYYY-MM-DD)
- `--json` — output raw JSONL instead of human-readable format

---

### `bmad log stats [--global]`

Aggregated performance report with percentiles and memory metrics.

```bash
bmad log stats
bmad log stats --global --since 2026-03-01
```

Output includes per-command count, avg/P50/P95/P99 duration, avg heap, peak RSS, error count, and top exec reasons.

---

### `bmad log patterns [--global]`

Exec pattern analysis — identifies recurring workarounds that should become native CLI commands.

```bash
bmad log patterns
bmad log patterns --global --min-count 3 --sort duration
```

Groups exec calls by reason, shows frequency, avg duration, avg child RSS, and estimates savings if native.

Flags:
- `--min-count <n>` — minimum occurrences to show (default: 2)
- `--sort <count|duration|memory>` — sort order

---

### `bmad log rotate [--global]`

Archive current log to `audit-YYYY-MM-DD.jsonl.gz` and start fresh.

```bash
bmad log rotate           # rotate project log
bmad log rotate --global  # rotate global log
```

---

## Audit Logging

Every `bmad` command writes a structured JSONL audit entry to two locations:

| Log | Path | Purpose |
|-----|------|---------|
| Project | `<docs-folder>/bmad-audit.jsonl` | Project-specific history |
| Global | `~/.bmad/audit.jsonl` | Cross-project pattern analysis |

Each entry includes:
- **Session**: PWD (session identifier), PID, hostname, bmad version, Go version
- **Command**: name, args, flags, raw string
- **Result**: exit code, stdout/stderr sizes, error type
- **Performance**: total/io_read/io_write/parse/logic milliseconds
- **Memory**: heap alloc, total alloc, GC count/pauses, goroutines, peak RSS, mallocs/frees
- **I/O**: files read/written, bytes, stories processed

For `bmad exec` entries, child process metrics are also captured: duration, CPU time (user/sys), peak RSS, exit code.

Global flags:
- `--no-log` — disable audit logging for this invocation
- `--max-log-size <size>` — auto-rotate when log exceeds size (e.g., `10MB`)

Full reference: [docs/logging.md](docs/logging.md)

## Error Handling

All commands exit non-zero on error and log structured errors via `uber/zap`. Sentinel errors are used for type-safe checking:

| Sentinel | Meaning | Exit code |
|---|---|---|
| `ErrStoryNotFound` | Story ID not in progress file | 1 |
| `ErrInvalidStatus` | Bad status string | 1 |
| `ErrInvalidGateResult` | Bad gate value in YAML | 1 |
| `ErrNoEligibleStory` | No pending story available | 2 |

## Development

```bash
git clone https://github.com/sosalejandro/bmad-story-runner-cli.git
cd bmad-story-runner-cli
go build ./...
go test ./...
```
