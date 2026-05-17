---
title: BMad Story Runner V6 — Specification
date: 2026-05-17
author: Alejandro Sosa
facilitator: BMad Master (Claude Opus 4.7) — design session 2026-05-16/17
status: spec — ready for Opus Plan Mode pressure-test
related:
  - docs/architecture/agent-architecture.md
  - https://github.com/sosalejandro/bmad-story-runner-cli
  - https://github.com/sosalejandro/claude-skills/
github_issues_addressed:
  - "#331 bmad-test-env-isolation skill"
  - "#332 nutrition-v2-go test-env pre-work"
  - "#333 P3 sprint-runner parallel + resource budget"
  - "#334 P1 validation (informs this spec)"
  - "#335 I2 code-as-discipline polish"
  - "#338 mobile-future resource handling"
---

# BMad Story Runner V6 — Specification

> **Purpose:** Specify the V6 architecture for autonomous BMad story execution with CLI-managed state, per-worktree isolation, per-story state machines, and orchestrator-driven L3 agent dispatch. Successor to V4 (`sosalejandro/bmad-story-runner-cli` + `sosalejandro/claude-skills`).

## 1. V4 heritage + V6 architectural delta

### What V4 nailed (PRESERVE)

| V4 capability | V6 disposition |
| ------------- | -------------- |
| Hexagonal Go architecture (domain/application/infrastructure/cmd) | KEEP — extend with new commands |
| `bmad-progress.json` per docs folder | KEEP — extended schema |
| State machine: `pending → in-progress → qa-review → complete \| blocked` | KEEP — add intermediate states (hydrating, reviewing, committing, env-up, env-down) |
| Prefix-matching story IDs | KEEP |
| QA gates (`qa/gates/*.yml`) | KEEP — output of test-reviewer L3 agent |
| `assign-groups` parallel distribution | KEEP — extend with file-overlap detection |
| Audit log via `bmad exec --reason` wrapper | KEEP |
| "CLI-first" rule (no inline jq/python) | KEEP |
| Autonomous policy pattern from `bmad-autonomous` skill (no interactive prompts) | ABSORB into V6 orchestrator agent |
| Retry budgets (max-qa-cycles, max-ci-retries) | KEEP + extend (max-tdd-cycles, max-review-iterations) |
| PR strategies (per-story, batch, end-only) | KEEP |
| Block + skip (don't halt sprint) | KEEP |

### V4 patterns we REPLACE

| V4 mechanism | V6 replacement | Why |
| ------------ | -------------- | --- |
| Step-skills (bmad-dev-dispatch, bmad-qa-dispatch, bmad-story-complete) | L3 agents (tdd-implementer, test-reviewer, code-reviewer, etc.) | Agents > skills for L3 (no skill-load drift; clean context per spawn) |
| `bmad-story-runner` orchestrator skill | Orchestrator AGENT (top-level in main session) | Same — persona-pinned protocol > loaded skill |
| `bmad-autonomous` wrapper skill | Autonomous policies ABSORBED into orchestrator agent | One file = one truth |
| `/BMad:agents:bmad-orchestrator` slash-command activation | DROP — V6 L3 agents invoke BMad skills directly via Skill tool | Simpler; no meta-orchestrator dependency |
| `*agent dev *develop` syntax | Skill invocation via Skill tool | Native Claude Code mechanism |
| V4 per-story files in flat `docs/stories/` folder | V6 lightweight stories in `epics.md` + JIT hydration via `story-hydrator` L3 agent | V6 BMad convention |

### V6 additions (V4 didn't have)

1. Worktree management per story (`bmad worktree create/destroy/list`)
2. Test-env Docker provisioning + cleanup (`bmad env up/down/cleanup-orphans`)
3. Resource budgeting + system-check (`bmad system-check`, `bmad config max-parallel`)
4. Story dependency graph (`depends_on:` frontmatter; `bmad next-actions --max-parallel N`)
5. Per-rule depguard flip tracking (`bmad depguard-flip`, `bmad depguard-status`)
6. Mobile-aware (per-story `requires_android: true` flag; orchestrator serializes)
7. TEA workflow integration (atdd-writer, test-automate, test-reviewer dispatched in STRICT mode)
8. Pragmatic vs Strict mode (broader than V4's QA strictness)
9. Per-story checkpoint mechanism (`bmad checkpoint`; user-confirm continue/adjust/halt)
10. JIT hydration support (`bmad hydrate <story-id>`)

### Architectural constraint that shapes V6

**Claude Code subagents cannot dispatch other subagents** (hypothesis B confirmed via P1 validation 2026-05-16). Consequence: the orchestrator MUST be top-level (user-invoked in main session); L3 agents are dispatched DIRECTLY by orchestrator; no intermediate L2 subagent layer.

Therefore:
- DELETE `.claude/agents/story-runner.md` (logic moves to orchestrator)
- DELETE `.claude/agents/sprint-runner.md` (logic moves to CLI + orchestrator)
- KEEP `.claude/agents/{story-hydrator,atdd-writer,tdd-implementer,test-automate,test-reviewer,code-reviewer}.md` (L3 agents)
- ADD `.claude/agents/orchestrator.md` (top-level, has Agent/Task tool for L3 dispatch)

## 2. V6 CLI command set

### V4 commands preserved (unchanged behavior)

```
bmad init <docs-folder>                          # scan stories, create progress JSON
bmad status <progress-json>                      # progress summary table
bmad scan <docs-folder>                          # list stories with task counts
bmad set-status <json> <id> <status>             # update story status
bmad set-complete <json> <id>                    # mark complete after CI passes
bmad bulk-complete <json> <id...>                # batch completion
bmad mark-story-file <file> <status>             # patch **Status:** in markdown
bmad add-concerns <json> <id> <json-array>       # append QA concerns
bmad write-gate <json> <id> <PASS|FAIL|CONCERNS> # write QA gate YAML
bmad gate-check <json>                           # read gate files, print results
bmad reconcile <json>                            # apply gate results to progress
bmad qa-pending <docs-folder>                    # list stories needing QA
bmad exec --reason "<why>" -- <cmd>              # audit-wrap arbitrary command
bmad log show|stats|patterns|rotate              # audit log viewing
```

### V4 commands extended

```
bmad next <json> [--group N --filter-group]
  → V6 EXTENSION: --max-parallel N
  → Returns parallel-eligible next-action list:
    [{ story_id, action: "hydrate|implement|review|...", env_config }, ...]

bmad list <json> [--group N --status <s> --unblocked-only --show-blockers]
  → V6 EXTENSION: --stage <current_stage>, --has-env, --mode <pragmatic|strict>

bmad assign-groups <json> <N>
  → V6 EXTENSION: detects file-overlap from story metadata `affects: [...]`;
    refuses to group stories with overlapping files in same parallel batch
```

### V6 new commands

```
# Worktree management
bmad worktree create <story-id>
  → Creates .worktrees/story-<id>/; returns worktree path + allocated branch name
bmad worktree destroy <story-id>
  → Deletes worktree + branch (after PR merge or explicit cleanup)
bmad worktree list
  → Active worktrees + their stories + state

# Test-env isolation
bmad env up <story-id> [--config .bmad-test-env.yml]
  → Reads per-repo config; allocates port set; renders docker-compose; brings up infra;
    returns env JSON { pg_port, redis_port, otel_port, db_name, container_ids }
bmad env down <story-id>
  → docker-compose down -v; releases port allocation
bmad env cleanup-orphans
  → Periodic sweep: finds [bmad-test-env]-tagged containers older than 4h; kills + cleans
bmad system-check [--reserve <ram_mb>]
  → Returns { free_ram_mb, cpu_load, max_safe_parallel: int }
  → --reserve flag accounts for user's other dev work

# JIT hydration
bmad hydrate <story-id>
  → Dispatches story-hydrator L3 agent; returns hydrated_file_path
  → Idempotent: refuses to re-hydrate existing file unless --re-hydrate

# Dependencies + planning
bmad dependency-graph
  → Visualizes dep graph from epics.md per-story frontmatter
bmad next-actions [--max-parallel N]
  → Returns parallel batch: [{ story, action, env_config }, ...]
    respecting deps + file-overlap + max-parallel + system resources

# Mode + config
bmad mode <pragmatic|strict>
  → Sets persistent mode for this docs folder
bmad config max-parallel <N>
  → Sets persistent parallelism cap
bmad config reserve-ram <MB>
  → Persistent reservation (e.g., 8000 = leave 8GB for other work)

# Depguard flip tracking
bmad depguard-flip <rule>
  → Marks per-rule global flip from warn → error; CI gates on this
bmad depguard-status
  → Shows per-rule flip state (warn/error per rule)

# Checkpoint mechanism
bmad checkpoint <story-id> [--user-confirm continue|adjust|halt]
  → Marks checkpoint reached; if --user-confirm halt → orchestrator pauses
bmad sprint-pause / bmad sprint-resume
  → User-initiated pause/resume of orchestrator
```

### Command-count summary

- V4: 18 commands
- V6 new: 16 commands (worktree:3, env:4, system:1, hydrate:1, deps:2, mode/config:3, depguard:2, checkpoint:3)
- V4 extended: 3 commands (next, list, assign-groups)
- **V6 total: ~37 commands**

## 3. State schema

### `bmad-progress.json` V6 extensions

```json
{
  "version": 2,                                    // bumped from V4's 1
  "docs_folder": "/abs/path/to/docs/stories",      // V4 preserved
  "last_updated": "2026-05-17T14:00:00Z",
  "mode": "pragmatic|strict",                      // V6 NEW (persistent)
  "max_parallel": 4,                               // V6 NEW (user-set cap)
  "reserve_ram_mb": 8000,                          // V6 NEW
  "depguard_flips": {                              // V6 NEW
    "no_infra_in_domain": "error",
    "isp_narrow_ports": "warn",
    "every_aggregate_eventrecorder": "warn",
    "every_service_withid": "warn"
  },
  "stories": [
    {
      "id": "4.1",                                 // V4 preserved
      "file": "4.1-identity-aggregates.md",        // V4 preserved
      "title": "Identity Aggregates with EventRecorder",
      "status": "complete|in-progress|blocked|pending|hydrating|reviewing|committing|env-up|env-down",  // V6 EXTENDED states
      "current_stage": "hydrate|atdd|implement|automate|test-review|code-review|commit|finish|done",   // V6 NEW (per-story state machine)
      "parallel_group": 1,                         // V4 preserved
      "assigned_session": "session-abc123",        // V4 preserved
      "blockers": [],                              // V4 preserved
      "depends_on": ["3.1", "3.2"],                // V6 NEW (story-level deps)
      "qa_concerns": [],                           // V4 preserved
      "ci_passed": true,                           // V4 preserved
      "completed_at": "2026-05-17T14:00:00Z",      // V4 preserved
      // V6 NEW fields:
      "worktree_path": ".worktrees/story-4.1",
      "branch_name": "story/4.1-identity-aggregates",
      "env_config": {
        "pg_port": 7611,
        "redis_port": 7612,
        "otel_port": 7613,
        "db_name": "story_4_1",
        "container_ids": ["bmad-test-env-4.1-pg", "bmad-test-env-4.1-redis"]
      },
      "hydrated_file": "_bmad-output/implementation-artifacts/stories/4.1-identity-aggregates.md",
      "resource_budget": { "ram_mb": 800, "cpu_cores": 0.6 },
      "requires_android": false,
      "retry_counts": { "tdd_cycles": 1, "qa_cycles": 0, "ci_retries": 0, "review_iterations": 1 },
      "commit_hash": "abc1234",
      "pr_url": "https://github.com/.../pull/501",
      "affects": ["src/contexts/identity/"]        // V6 NEW (for file-overlap parallel detection)
    }
  ]
}
```

### New state files

```
.worktree-allocations.yaml          # active worktree → story_id → port_range mapping
.env.test (per worktree)            # env vars (PG_PORT, REDIS_PORT, etc.) for that story
.bmad-test-env.yml (per repo)       # project test-env config (port range, services, defaults)
```

### `.bmad-test-env.yml` (per-repo) schema

```yaml
port_range: { start: 7600, end: 7799 }
ports_per_story: 10  # block size; each story gets a 10-port block from range

services:
  - name: postgres
    image: postgres:15-alpine
    resource_limits: { ram_mb: 256, cpu_cores: 0.25 }
    env: { POSTGRES_PASSWORD: test, POSTGRES_DB: "${DB_NAME}" }
    healthcheck: { test: pg_isready, timeout: 30s }
  - name: redis
    image: redis:7-alpine
    resource_limits: { ram_mb: 64, cpu_cores: 0.1 }
    healthcheck: { test: redis-cli ping, timeout: 10s }
  - name: otel-collector
    image: otel/opentelemetry-collector:latest
    resource_limits: { ram_mb: 128, cpu_cores: 0.15 }

default_resource_budget_by_stack:
  go-backend: { ram_mb: 800, cpu_cores: 0.6 }
  web-e2e:    { ram_mb: 200, cpu_cores: 0.2 }
  mobile-e2e: { ram_mb: 3500, cpu_cores: 1.5, requires_android: true }

container_label: "[bmad-test-env]"   # for sweeper to identify orphans
orphan_age_hours: 4                  # sweep threshold

mobile:                              # future; per #338
  android_emulator: { enabled: false }
```

## 4. Orchestrator agent system prompt structure

Located at `.claude/agents/orchestrator.md`. Key structure:

```markdown
---
name: orchestrator
description: "Drives BMad v6 story execution end-to-end. Autonomous loop: queries CLI for next-actions, dispatches L3 stage agents in parallel batches, manages per-worktree test-env lifecycle, handles retry budgets + checkpoints. Use when told 'run the sprint [mode]' or 'execute stories autonomously'."
tools: Bash, Read, Edit, Write, Task
skills:
  - sprint-planning
  - story-checkpoint
  - test-env-isolation
  - smart-commit
  - open-pr
---

# BMad v6 Orchestrator

You are a BMad v6 story-execution orchestrator. AUTONOMOUS LOOP — no user prompts mid-flight except at checkpoints.

## Hard persona rules

- ❌ NEVER do story code work yourself (no impl, no test-writing, no review)
- ❌ NEVER skip the test-env teardown (try/finally per story)
- ❌ NEVER advance past a story without updating CLI state
- ❌ NEVER dispatch a story whose deps aren't satisfied (CLI validates)
- ✅ Dispatch L3 stage agents via Task tool (Agent / multi-Task in single message for parallel)
- ✅ Use bmad CLI for ALL state mutations + queries (no inline jq/yaml)
- ✅ Honor retry budgets per stage; mark blocked + continue on exhaustion
- ✅ Run try/finally for per-story env (env up → work → env down ALWAYS)

## Inputs

- `mode` (default `pragmatic`) — `pragmatic | strict`
- `max-parallel` (default from CLI config)
- `pr-strategy` (default `per-story`) — per-story | batch | end-only
- `batch-size` (when pr-strategy=batch; default 3)
- `max-tdd-cycles` (default 3)
- `max-qa-cycles` (default 3)
- `max-ci-retries` (default 2)
- `max-review-iterations` (default 3 — strict mode; pragmatic = 1)
- `checkpoint-after-stories` (default 4)

## Autonomous loop

```
Loop:
  1. CLI: bmad system-check --reserve <reserve_ram>
     → { free_ram_mb, max_safe_parallel }
  2. effective_parallel = MIN(max-parallel-config, max_safe_parallel, mode-cap)
     where mode-cap = 4 pragmatic, 2 strict (drift tolerance)
  3. CLI: bmad next-actions --max-parallel <effective_parallel>
     → batch = [{ story_id, action: hydrate|implement|review|..., env_config? }, ...]
  4. If batch empty:
     a. If checkpoint reached → invoke story-checkpoint skill → halt for user-confirm
     b. If all done → final PR strategy + exit
     c. Otherwise → break (sprint exhausted)
  5. PARALLEL DISPATCH (single message, N Task calls):
     For each batch item:
       a. If story has no env yet AND action != hydrate:
          CLI: bmad env up <story_id> → env_config
       b. Dispatch L3 agent matching action:
          Task({ subagent_type: <agent>, prompt: "<protocol with env_config, hydrated_file, mode>" })
  6. Wait for ALL N returns
  7. For each return:
     - If status=ok: CLI: bmad set-status <id> <next_stage_or_complete>
     - If status=blocked:
       - Increment retry counter
       - If budget exhausted: CLI: bmad set-status <id> blocked; CLI: bmad env down <id> (try/finally)
       - Else: CLI: bmad set-status <id> <same_stage_pending_retry>
     - If status=done AND env present: CLI: bmad env down <id>; CLI: bmad set-complete <id>
  8. Goto Loop
```

## Mode-driven dispatch

**Pragmatic** stages per story: hydrate → tdd-implement → code-review → commit
**Strict** stages per story: hydrate → atdd → tdd-implement → test-automate → test-review → code-review → commit
  + STRICT: code-review iterates till clean (max-review-iterations); pragmatic = 1 round

## Try/finally infra lifecycle

For each story, regardless of success/failure:
1. CLI: bmad env up <story> → env_config (allocates ports, brings up Docker)
2. <do work via L3 dispatches>
3. CLI: bmad env down <story> — ALWAYS, even on failure
4. CLI: bmad worktree destroy <story> — after PR opened (or on user-confirm)

Exception handler at orchestrator level: on crash/interrupt, iterate active stories + run env-down for each.

## Checkpoint behavior

After every `checkpoint-after-stories` stories complete:
1. CLI: bmad status (overview)
2. Invoke `story-checkpoint` skill → produces summary of last N stories + drift assessment
3. HALT — emit summary + ask user: continue | adjust | halt
4. User responds → CLI: bmad sprint-resume → continue OR CLI: bmad sprint-pause → exit

## Failure handling per stage

| Stage | On blocked | Retry budget | Exhausted action |
| ----- | ---------- | ------------ | ---------------- |
| hydrate | CLI: bmad add-concerns + bmad set-status blocked | 1 (deterministic) | Mark blocked; env not yet up so no teardown needed |
| atdd / test-automate | CLI: bmad add-concerns | 1 | Mark blocked; env down |
| tdd-implement | CLI: bmad add-concerns | max-tdd-cycles (default 3) | Mark blocked; env down |
| test-review | CLI: bmad add-concerns | re-dispatch tdd-implement (max-qa-cycles) | Mark blocked; env down |
| code-review | If iterate: re-dispatch tdd-implement | max-review-iterations | Mark blocked; env down |
| commit / CI | If task check fails: re-engage tdd-implement | max-ci-retries | Mark blocked; env down |

## Output (return JSON to user)

```json
{
  "mode": "pragmatic",
  "total_stories": 180,
  "completed": ["1.1", "1.2", ...],
  "blocked": [{ "story_id": "4.7", "reason": "..." }],
  "in_progress": [],
  "checkpoint_reached": false,
  "duration_minutes": 240
}
```
```

## 5. Composable skills

### `sprint-planning` skill

**Purpose:** Read `epics.md` → build dependency graph → produce sprint plan (ordered batches).

```markdown
---
name: sprint-planning
description: "Builds a sprint dependency graph + ordered batch plan from epics.md. Honors per-story depends_on + file-overlap + parallel cap. Use when starting a new sprint or planning a re-batch after blockers."
tools: Bash, Read
---

# Sprint Planning

INPUT: path to epics.md + max-parallel + per-repo file-overlap conventions

PROTOCOL:
1. Parse epics.md → extract stories + frontmatter (depends_on, affects, resource_budget, requires_android)
2. Build directed dependency graph
3. Topo-sort respecting deps
4. Within each topo level: group by NON-overlapping file sets (use `affects:` frontmatter or per-BC heuristic)
5. Apply parallel cap per batch
6. Mobile serialization: stories with `requires_android: true` get their own batch (or share with non-mobile if not Android-bound)
7. Resource budget sanity-check: per batch, sum resource_budget; warn if exceeds system max
8. Return plan JSON: { batches: [[story_ids], ...], total_stories, estimated_duration }

OUTPUT: Sprint plan JSON; orchestrator consumes via CLI cache (`bmad sprint-plan apply`).
```

### `story-checkpoint` skill

**Purpose:** Mid-sprint review — summarize last N stories + assess drift + present user options.

```markdown
---
name: story-checkpoint
description: "Mid-sprint review after N stories. Summarizes last batch, assesses drift, presents continue/adjust/halt options. Use when orchestrator hits checkpoint-after-stories threshold."
tools: Bash, Read
---

# Story Checkpoint

INPUT: progress-json path + last-N-stories-completed

PROTOCOL:
1. CLI: bmad status (current overall)
2. For each of last N stories:
   - Read hydrated_file's progress markers + commit hash + PR URL
   - Note: any stages that exceeded retry budget? Any QA concerns appended?
3. Drift assessment:
   - Did any story's implementation deviate from its hydrated spec? (heuristic: check commit message vs story title; check files modified vs `affects:`)
   - Did any story introduce changes that affect upstream story dependencies?
4. Summary report:
   - N stories completed
   - X stories blocked + reasons
   - Y total commits
   - Z total PRs
   - Drift signals: [...]
5. HALT with user prompt: continue | adjust epic plan | halt sprint
6. User response → CLI: bmad sprint-resume (continue) OR bmad sprint-pause (adjust/halt)
```

### `test-env-isolation` skill (system-wide; promote to `~/.claude/skills/`)

**Purpose:** Cross-project skill providing port-pool + Docker scripts for per-story test infrastructure.

```markdown
---
name: test-env-isolation
description: "Per-story test infrastructure isolation via Docker. Provides port-pool allocation + docker-compose up/down + cleanup scripts. Requires per-repo .bmad-test-env.yml. Use when story-runner needs isolated DB/Redis/OTel infra per parallel story."
tools: Bash, Read, Write
---

# Test-Env Isolation (cross-project)

INPUT: story_id + path to .bmad-test-env.yml

PROTOCOL (env up):
1. Read .bmad-test-env.yml → port_range + services + resource_limits
2. CLI: bmad worktree create <story_id> → worktree_path
3. CLI port-pool: allocate next ports_per_story block from range
4. Render env: write .env.test in worktree with PG_PORT=, REDIS_PORT=, OTEL_PORT=, DB_NAME=story_<id>
5. Bash: docker-compose -f docker-compose.test.yml --env-file .env.test up -d
6. Wait for healthchecks (timeout per service)
7. Tag containers with [bmad-test-env]-<story_id>
8. Return env_config JSON: { worktree_path, pg_port, redis_port, otel_port, db_name, container_ids }

PROTOCOL (env down):
1. Bash: docker-compose -f docker-compose.test.yml --env-file .env.test down -v
2. CLI port-pool: release ports back
3. CLI worktree destroy <story_id> (optional — orchestrator may keep worktree for PR)
4. Return success/failure

PROTOCOL (cleanup-orphans):
1. Bash: docker ps -a --filter "label=bmad-test-env"
2. For each: check age (creation time vs threshold from .bmad-test-env.yml)
3. If older than orphan_age_hours: down + remove
4. Return: orphans cleaned count
```

## 6. L3 agent inventory (UNCHANGED from PR #337)

Already at `.claude/agents/`:

- `story-hydrator.md` — JIT hydration via bmad-bmm-create-story
- `atdd-writer.md` — STRICT only; bmad-tea-testarch-atdd
- `tdd-implementer.md` — TDD via bmad-bmm-dev-story + superpowers
- `test-automate.md` — STRICT only; bmad-tea-testarch-automate
- `test-reviewer.md` — STRICT only; bmad-tea-testarch-test-review
- `code-reviewer.md` — bmad-bmm-code-review

DELETE from PR #337:
- `story-runner.md` — protocol absorbed into orchestrator
- `sprint-runner.md` — logic moved to CLI + sprint-planning skill

ADD:
- `orchestrator.md` — see §4 above

## 7. Per-repo config schema

See §3 `.bmad-test-env.yml` schema. Plus per-story frontmatter additions:

```yaml
# In a story's frontmatter (within epics.md story sections)
---
story_id: "4.1"
depends_on: ["3.1", "3.2"]  # other story IDs (or empty)
affects:                     # files this story touches (for file-overlap detection)
  - src/contexts/identity/
resource_budget:
  ram_mb: 800
  cpu_cores: 0.6
requires_android: false      # per #338 mobile flag
---
```

## 8. Migration path V4 → V6

### For your existing V4 skills + CLI

**V4 CLI (`bmad-story-runner-cli`):** Extend in-place (V6 = v2.0).
- New commands added; V4 commands preserved
- Schema migration: V4 `bmad-progress.json` (version 1) → V6 (version 2) via `bmad migrate` command
- Backward compat: V4 projects without `.bmad-test-env.yml` operate in "no-isolation mode" (sequential, no Docker provisioning)

**V4 skills (`claude-skills/skills/bmad-*`):** Deprecate gradually.
- `bmad-story-runner` skill → REPLACED by V6 orchestrator agent
- `bmad-autonomous` skill → ABSORBED into V6 orchestrator agent
- `bmad-dev-dispatch` skill → REPLACED by V6 `tdd-implementer` L3 agent
- `bmad-qa-dispatch` skill → REPLACED by V6 `test-reviewer` L3 agent
- `bmad-code-review` skill → REPLACED by V6 `code-reviewer` L3 agent
- `bmad-session-init` skill → ABSORBED into V6 orchestrator agent (init logic)
- `bmad-story-complete` skill → ABSORBED into V6 orchestrator agent (commit + PR logic)
- `bmad-exec-guard` skill → KEEP as-is (orthogonal; still useful for non-CLI command logging)

**For users on V4:** continue using V4 stack for existing projects; migrate to V6 when ready (new project OR opt-in upgrade).

## 9. Mobile-future hooks (per #338)

V6 design accommodates mobile re-engagement without rewrite:

- `requires_android: true` flag in story metadata
- Orchestrator serializes mobile-requiring stories (own batch; or shared android-emulator across them)
- Per-repo `.bmad-test-env.yml` `mobile:` section (currently `enabled: false`)
- When mobile resumes (per #338):
  - Build `android-emulator-manager` skill OR L3 agent
  - Per-repo mobile-section gets populated (emulator image, AVD config, ADB port range)
  - Stories with `requires_android: true` start dispatching alongside backend stories with serialization

## 10. Test plan (how to validate V6)

Pre-build validation:

1. **CLI commands compile + return correct schemas** (Go unit tests on V6 extensions)
2. **State machine transitions valid** (CLI rejects invalid transitions; unit tests)
3. **Per-repo config parses correctly** (integration test with sample `.bmad-test-env.yml`)
4. **Worktree create/destroy round-trips cleanly** (integration test; verify git state)
5. **Test-env up/down provisions + tears down Docker** (integration test using sample compose file)
6. **System-check returns sensible values** (integration test; verify against `free`/`ps` output)

Build validation:

7. **Orchestrator agent persona-pinning** — invoke on a no-op story (e.g., "echo hello"); verify it dispatches L3 (story-hydrator) instead of doing work
8. **L3 dispatch works from orchestrator** — verify Task tool invocation succeeds (hypothesis A test for orchestrator-as-top-level)
9. **Try/finally infra lifecycle** — inject a failure mid-story; verify env-down runs anyway
10. **Retry budget enforcement** — force max-tdd-cycles exceeded; verify blocked + continue
11. **Checkpoint mechanism** — run sprint past checkpoint threshold; verify halt + user-confirm pattern
12. **Parallel batch dispatch** — run 2-3 parallel stories; verify state consistency (no race conditions)

End-to-end validation:

13. **Run actual Story 1.1** (canonical reference impl) through the V6 orchestrator pragmatic mode
14. **Observe behavior**: did orchestrator stay persona-pinned? Did L3 agents work cleanly? Did sprint-status.yaml update? Did PR open?
15. **Stretch: 2-3 parallel stories** with test-env isolation; verify no port collisions, no shared-state issues

## 11. Open questions for Opus Plan Mode pressure-test

1. **CLI command set size**: 37 commands is substantial. Is this the right granularity, or are we over-componentizing? Could some commands collapse (e.g., env up/down/cleanup into `bmad env <verb>`)?

2. **State file format**: JSON for `bmad-progress.json` (V4 inheritance) vs SQLite for richer queries + ACID guarantees as parallel writers grow. Worth the migration cost?

3. **Orchestrator agent system prompt size**: §4 sketch is dense. Will it fit reliably in agent context budget across many story iterations? Or do we need to split into orchestrator + dispatcher skills?

4. **Test-env-isolation skill granularity**: per-skill (test-env-isolation does ALL infra concerns) vs per-concern (separate skills for port-pool, docker-up, healthchecks, sweeper). SRP says split; convenience says combine.

5. **Checkpoint cadence**: every 4 stories is arbitrary. Should it adapt to story complexity (e.g., checkpoint after every N stories OR after a "complex" story like meal-prep)?

6. **Failure modes**: orchestrator crash mid-batch. Recovery story: resume from CLI state. But what if Docker containers were brought up + orchestrator died before recording env_config in state? Stale orphans. Cleanup-orphans handles, but is it enough?

7. **Mobile lifecycle**: defer per #338. But is there a design question we should answer NOW that constrains V6 to be mobile-compatible later?

8. **Cross-project portability**: is the test-env-isolation skill truly cross-project? Other projects may have different infra (mongo instead of postgres; rabbitmq instead of redis). The `.bmad-test-env.yml` accommodates this but template proliferation could be a maintenance burden.

9. **Migration cost for V4 users**: is the `bmad migrate` command sufficient, or do users need a hand-holding doc?

10. **Agent-teams future**: V6 orchestrator-subagent model is the right call now. When/if agent-teams becomes useful (retrospectives, design sessions per architecture doc), how does it integrate without disrupting V6 orchestrator?

11. **CI integration**: does the V6 orchestrator run as part of CI (e.g., nightly sprint execution)? Or is it strictly local-developer? If CI, what's the headless invocation pattern?

12. **Cost/observability**: each L3 agent dispatch consumes Claude tokens. At parallel-4 across many stories, costs accumulate. Should V6 track per-story token costs in `bmad-progress.json` for budget awareness?

## Spec sign-off

This spec captures the design as of 2026-05-17. Ready for Opus Plan Mode pressure-test against the 12 open questions above. Build phase begins after Plan Mode review + user approval.
