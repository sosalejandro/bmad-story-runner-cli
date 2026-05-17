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

## 2. V6 CLI command set (verb-namespaced per §12.1)

Each `bmad` subtree is one Go package under `cmd/`. Root-level commands are cross-cutting; verb-namespaced commands group around a single SRP unit (story, sprint, env, worktree, depguard, gate). State store is SQLite (`bmad-state.db` per docs-folder); no positional JSON path arg required — commands resolve state from cwd or `--state` override.

### Root-level (cross-cutters)

```
bmad init [<docs-folder>]                   # scaffold bmad-state.db + scan stories
bmad migrate [--from <progress.json>]       # V4 progress.json → V6 SQLite, one-shot
bmad system-check [--reserve <ram_mb>]      # { free_ram_mb, cpu_load, max_safe_parallel }
bmad config <key> [<value>]                 # get/set persistent config (mode, max_parallel, reserve_ram_mb, checkpoint.count_threshold, env.stale_threshold_minutes)
bmad dispatch <stage> <story-id>            # manual L3 invocation (escape hatch)
bmad exec --reason "<why>" -- <cmd>         # audit-wrap arbitrary command (V4 preserved)
bmad log <show|stats|patterns|rotate>       # audit log viewing (V4 preserved)
```

### `bmad story <verb>` (per-story lifecycle)

```
bmad story status [<story-id>]              # one-row or table view; filters: --stage --has-env --status
bmad story hydrate <story-id> [--re-hydrate]
bmad story next [--max-parallel N]          # parallel-eligible next-action batch
bmad story checkpoint <story-id>            # mark checkpoint reached
bmad story set-status <story-id> <status>   # admin escape; also patches markdown frontmatter
bmad story complete <story-id...>           # variadic; folds V4 bulk-complete
```

### `bmad sprint <verb>` (sprint-level orchestration)

```
bmad sprint plan [--assign-groups N]        # invokes sprint-planning skill; persists batches
bmad sprint run [--mode pragmatic|strict]   # launches orchestrator (autonomous loop)
bmad sprint pause                           # halt orchestrator after current dispatch returns
bmad sprint resume                          # continue after pause or checkpoint
bmad sprint status                          # sprint-aggregate view (batches, blocked, in-flight, tokens-spent)
```

### `bmad env <verb>` (per-story test infrastructure)

```
bmad env up <story-id>                      # allocates ports + renders .env.test + docker-compose up + healthchecks
bmad env down <story-id>                    # docker-compose down -v + releases ports + marks env_allocations reclaimed
bmad env status [<story-id>]                # active envs, port allocations, activity-probe age
bmad env cleanup-orphans                    # sweeper: activity-based stale detection per §12.6
```

### `bmad worktree <verb>` (worktree lifecycle)

```
bmad worktree create <story-id>             # .worktrees/story-<id>/ + branch
bmad worktree destroy <story-id>            # delete worktree + branch (post-PR or explicit)
bmad worktree list                          # active worktrees + stories + state
bmad worktree prune                         # delete worktrees whose stories are complete + merged
```

### `bmad depguard <verb>` (per-rule flip tracking)

```
bmad depguard flip <rule>                   # warn → error; persisted in SQLite + CI gates on it
bmad depguard status                        # per-rule flip state
bmad depguard history                       # flip timeline per rule
```

### `bmad gate <verb>` (QA gate, V4 surface preserved)

```
bmad gate write <story-id> <PASS|FAIL|CONCERNS> [--concerns <json-array>]   # folds V4 write-gate + add-concerns
bmad gate check                             # read gate files, print results
bmad gate reconcile                         # apply gate results to story status
```

### Command-count summary

- Root cross-cutters: 7
- `story` namespace: 6
- `sprint` namespace: 5
- `env` namespace: 4
- `worktree` namespace: 4
- `depguard` namespace: 3
- `gate` namespace: 3
- **V6 total: 32 commands** (down from V4-extended sketch of 37)

Each namespace stays at or below ~6 verbs. If a namespace grows past that during build, treat it as a smell — split or relocate verbs.

### V4 → V6 mapping (for migration users)

| V4 command                    | V6 equivalent                                     |
| ----------------------------- | ------------------------------------------------- |
| `bmad status <json>`          | `bmad story status` + `bmad sprint status`        |
| `bmad scan <docs>`            | `bmad story status` (no story-id → table view)    |
| `bmad set-status <json> <id>` | `bmad story set-status <id>`                      |
| `bmad set-complete <json>`    | `bmad story complete <id>`                        |
| `bmad bulk-complete`          | `bmad story complete <id1> <id2> ...`             |
| `bmad mark-story-file`        | (folded into `bmad story set-status` — atomic)    |
| `bmad add-concerns`           | (folded into `bmad gate write --concerns`)        |
| `bmad write-gate`             | `bmad gate write`                                 |
| `bmad gate-check`             | `bmad gate check`                                 |
| `bmad reconcile`              | `bmad gate reconcile`                             |
| `bmad qa-pending`             | `bmad story status --stage qa`                    |
| `bmad next`                   | `bmad story next`                                 |
| `bmad list`                   | `bmad story status` (with filter flags)           |
| `bmad assign-groups`          | `bmad sprint plan --assign-groups N`              |
| `bmad mode <p\|s>`            | `bmad config mode <p\|s>`                         |

## 3. State schema — SQLite (per §12.2)

State store: `bmad-state.db` per docs-folder. SQLite + WAL mode. One writer per process (orchestrator + CLI sub-commands serialize through file lock); read-only consumers use a read-only connection.

V4 `bmad-progress.json` is **read-only** input to `bmad migrate`; V6 writes only to SQLite. The two never coexist as authoritative state.

### Schema layout

Lives at `infrastructure/state/sqlite/schema/`, golang-migrate style — numbered `.sql` files (`0001_initial.up.sql`, `0001_initial.down.sql`, etc.). Schema version recorded in `schema_version` table; runtime refuses to open a DB whose version is newer than the binary supports.

### Initial schema (`0001_initial.up.sql`)

```sql
CREATE TABLE schema_version (
  version    INTEGER PRIMARY KEY,
  applied_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- ---------- Config (key/value) ----------
CREATE TABLE config (
  key        TEXT PRIMARY KEY,
  value      TEXT NOT NULL,
  updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
-- Seeded on `bmad init`:
--   docs_folder, mode (pragmatic|strict), max_parallel, reserve_ram_mb,
--   pr_strategy, batch_size, max_tdd_cycles, max_qa_cycles, max_ci_retries,
--   max_review_iterations, checkpoint.count_threshold (default 4),
--   env.stale_threshold_minutes (default 120)

-- ---------- Stories ----------
CREATE TABLE stories (
  id                         TEXT PRIMARY KEY,         -- "4.1"
  file                       TEXT NOT NULL,            -- relative path under docs_folder
  title                      TEXT NOT NULL,
  status                     TEXT NOT NULL,            -- pending|hydrating|in-progress|reviewing|committing|env-up|env-down|complete|blocked
  current_stage              TEXT,                     -- hydrate|atdd|implement|automate|test-review|code-review|commit|finish|done
  parallel_group             INTEGER,
  hydrated_file              TEXT,
  resource_budget_ram_mb     INTEGER,
  resource_budget_cpu_cores  REAL,
  requires_android           INTEGER NOT NULL DEFAULT 0,  -- SQLite BOOLEAN
  complexity                 TEXT NOT NULL DEFAULT 'medium',  -- low|medium|high (drives §12.5 trigger)
  commit_hash                TEXT,
  pr_url                     TEXT,
  ci_passed                  INTEGER NOT NULL DEFAULT 0,
  completed_at               TIMESTAMP,
  created_at                 TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at                 TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE story_dependencies (
  story_id      TEXT NOT NULL REFERENCES stories(id) ON DELETE CASCADE,
  depends_on_id TEXT NOT NULL REFERENCES stories(id) ON DELETE CASCADE,
  PRIMARY KEY (story_id, depends_on_id)
);

CREATE TABLE story_affects (                 -- paths a story touches (file-overlap detection)
  story_id TEXT NOT NULL REFERENCES stories(id) ON DELETE CASCADE,
  path     TEXT NOT NULL,
  PRIMARY KEY (story_id, path)
);

CREATE TABLE story_concerns (                -- QA concerns appended over time
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  story_id    TEXT NOT NULL REFERENCES stories(id) ON DELETE CASCADE,
  appended_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  source      TEXT NOT NULL,                 -- agent role that flagged
  body_json   TEXT NOT NULL
);

CREATE TABLE story_retry_counts (
  story_id            TEXT PRIMARY KEY REFERENCES stories(id) ON DELETE CASCADE,
  tdd_cycles          INTEGER NOT NULL DEFAULT 0,
  qa_cycles           INTEGER NOT NULL DEFAULT 0,
  ci_retries          INTEGER NOT NULL DEFAULT 0,
  review_iterations   INTEGER NOT NULL DEFAULT 0
);

-- ---------- Batches (sprint-planning output) ----------
CREATE TABLE batches (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  sequence_no  INTEGER NOT NULL UNIQUE,      -- batch order in sprint
  status       TEXT NOT NULL,                -- planned|in-flight|complete
  created_at   TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  started_at   TIMESTAMP,
  completed_at TIMESTAMP
);

CREATE TABLE batch_stories (
  batch_id INTEGER NOT NULL REFERENCES batches(id) ON DELETE CASCADE,
  story_id TEXT    NOT NULL REFERENCES stories(id) ON DELETE CASCADE,
  PRIMARY KEY (batch_id, story_id)
);

-- ---------- Worktrees ----------
CREATE TABLE worktrees (
  story_id         TEXT PRIMARY KEY REFERENCES stories(id) ON DELETE CASCADE,
  path             TEXT NOT NULL UNIQUE,
  branch_name      TEXT NOT NULL UNIQUE,
  created_at       TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  last_activity_at TIMESTAMP                 -- refreshed by `env status` activity probe
);

-- ---------- Test-env allocations ----------
CREATE TABLE env_allocations (
  story_id       TEXT PRIMARY KEY REFERENCES stories(id) ON DELETE CASCADE,
  pg_port        INTEGER NOT NULL,
  redis_port     INTEGER NOT NULL,
  otel_port      INTEGER,
  db_name        TEXT NOT NULL,
  container_ids  TEXT NOT NULL,               -- JSON array
  created_at     TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  reclaimed_at   TIMESTAMP,                   -- non-NULL after sweeper teardown
  reclaim_reason TEXT                         -- 'completed' | 'stale' | 'manual'
);

CREATE INDEX env_allocations_reclaimed_idx ON env_allocations(reclaimed_at);

-- ---------- Dispatches (one row per L3 agent invocation; §12.7 cost tracking) ----------
CREATE TABLE dispatches (
  id                   INTEGER PRIMARY KEY AUTOINCREMENT,
  story_id             TEXT NOT NULL REFERENCES stories(id) ON DELETE CASCADE,
  stage                TEXT NOT NULL,         -- hydrate|atdd|implement|automate|test-review|code-review|commit
  agent_role           TEXT NOT NULL,         -- L3 agent name
  attempt_no           INTEGER NOT NULL,
  status               TEXT NOT NULL,         -- ok|blocked|errored
  reason               TEXT,
  input_tokens         INTEGER NOT NULL DEFAULT 0,
  output_tokens        INTEGER NOT NULL DEFAULT 0,
  cache_read_tokens    INTEGER NOT NULL DEFAULT 0,
  cache_create_tokens  INTEGER NOT NULL DEFAULT 0,
  model                TEXT,
  duration_ms          INTEGER NOT NULL DEFAULT 0,
  dispatched_at        TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  returned_at          TIMESTAMP
);

CREATE INDEX dispatches_story_idx ON dispatches(story_id, stage);

-- ---------- Checkpoints (§12.5) ----------
CREATE TABLE checkpoints (
  id                   INTEGER PRIMARY KEY AUTOINCREMENT,
  triggered_at         TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  trigger_kind         TEXT NOT NULL,        -- 'count' | 'complexity'
  trigger_detail       TEXT,                 -- e.g. complexity:<story-id> or NULL
  stories_since_last   INTEGER NOT NULL,
  user_decision        TEXT,                 -- 'continue' | 'adjust' | 'halt'
  decided_at           TIMESTAMP,
  summary_json         TEXT NOT NULL         -- full checkpoint payload
);

-- ---------- Depguard flips ----------
CREATE TABLE depguard_flips (
  rule       TEXT PRIMARY KEY,
  state      TEXT NOT NULL,                  -- 'warn' | 'error'
  flipped_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE depguard_flip_history (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  rule       TEXT NOT NULL,
  from_state TEXT NOT NULL,
  to_state   TEXT NOT NULL,
  flipped_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  reason     TEXT
);
```

### Filesystem artifacts that remain non-DB

```
bmad-state.db                       # SQLite state store (this section)
.env.test (per worktree)            # env vars rendered by `env up` for docker-compose
.bmad-test-env.yml (per repo)       # project test-env config (port range, services, defaults)
docker-compose.test.yml (per repo)  # project test infra topology (image set + label)
```

`.worktree-allocations.yaml` from the V4-shaped sketch is **dropped** — `worktrees` + `env_allocations` tables replace it.

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

mobile:                              # future; per #338
  android_emulator: { enabled: false }
```

Note: `orphan_age_hours` from the V4-shaped sketch is **dropped** — replaced by activity-based detection per §12.6 (`env.stale_threshold_minutes` in `config` table). Pure age-based sweep was too aggressive for the debugging workflow.

### Adapter layout

```
domain/state/
  Store         # interface — write-once contract; both adapters satisfy it
  Stories       # narrow port (ISP) for story queries
  Dispatches    # narrow port for dispatch insert + cost rollup
  Envs          # narrow port for env allocation lifecycle
  Worktrees     # narrow port for worktree lifecycle
  Config        # narrow port for config get/set
  Depguard      # narrow port for flip state
  Checkpoints   # narrow port for checkpoint record/decision
infrastructure/state/sqlite/        # the V6 adapter
infrastructure/state/json/          # V4 read-only adapter (used by `bmad migrate` only)
infrastructure/state/sqlite/schema/ # numbered migration files
```

ISP-keystone: no consumer pulls `domain/state.Store` whole; each command/skill depends on the narrow port for its concern.

## 4. Orchestrator — template + render pipeline (per §12.3)

The orchestrator does NOT carry a monolithic system prompt. Instead, prompts are **versioned templates** rendered by the CLI per dispatch. Each agent invocation gets exactly the context it needs, no more.

### Why templating

A single fat orchestrator prompt accumulates context cruft over a 180-story sprint: persona rules, lifecycle rules, mode rules, stage-specific instructions, prior-stage output, retry context. It hits context budget limits and becomes diff-unreviewable. Templating splits each concern into a file with a defined slot contract — same hygiene we apply to code.

### Pipeline shape

```
                                +--------------------------+
  bmad sprint run --mode <m> -->|   orchestrator process   |
                                +--------------------------+
                                            |
                            +---------------+---------------+
                            |  per autonomous loop iteration |
                            +---------------+---------------+
                                            v
                  bmad render orchestrator-loop --mode <m> ...
                                            |
                                            v
                  +--------------- prompts/renderer.go ------------+
                  |  load template -> resolve slots from sqlite +  |
                  |  filesystem -> emit rendered text              |
                  +-----------------+------------------------------+
                                    v
                          stdout: rendered prompt
                                    |
                                    v
              orchestrator: Task({ subagent_type: <l3>,
                                   prompt: <rendered text> })
```

Three layers, three SRP units:

1. **`domain/dispatch/`** — the autonomous loop: "what stage runs next, against which story, with which prior context." Pure logic; no IO.
2. **`infrastructure/prompts/`** — template registry + renderer (`text/template`). Owns load, slot resolution, rendering. Knows nothing about dispatch logic.
3. **`infrastructure/agent/`** — Claude Code Agent-tool invoker. Receives rendered prompts; doesn't compose them.

### Template inventory

Lives at `infrastructure/prompts/templates/`. One file per concern:

```
templates/
  orchestrator_loop.tmpl       # per-iteration plan: parallel dispatch + retry handling rules
  stage_hydrate.tmpl           # prompt for story-hydrator L3
  stage_atdd.tmpl              # prompt for atdd-writer L3 (strict mode)
  stage_implement.tmpl         # prompt for tdd-implementer L3
  stage_test_automate.tmpl     # prompt for test-automate L3 (strict mode)
  stage_test_review.tmpl       # prompt for test-reviewer L3 (strict mode)
  stage_code_review.tmpl       # prompt for code-reviewer L3
  stage_commit.tmpl            # prompt for commit + PR step
  checkpoint_summary.tmpl      # prompt for story-checkpoint skill render
  retry_context.tmpl           # injected as a sub-block when retry_attempt > 0
```

### Slot contract (example: `stage_implement.tmpl`)

```text
{{/* Slot contract:
       .StoryID         (string)   required
       .HydratedFile    (string)   required — path on disk
       .Mode            (string)   pragmatic|strict
       .EnvConfig       (struct)   pg_port, redis_port, otel_port, db_name
       .PriorAttempt    (struct)   nil on first try; populated with last failure on retry
       .EpicContext     (string)   optional excerpt of epic file
*/}}

# Implement Story {{.StoryID}}

Hydrated spec: {{.HydratedFile}}
Mode: {{.Mode}}

## Test environment (already up — do NOT recreate)

- PostgreSQL on :{{.EnvConfig.PgPort}}  (db: {{.EnvConfig.DbName}})
- Redis on :{{.EnvConfig.RedisPort}}
- OTEL on :{{.EnvConfig.OtelPort}}

{{if .PriorAttempt -}}
{{template "retry_context.tmpl" .PriorAttempt}}
{{- end}}

## What to do

Read the hydrated spec. Implement TDD per the checklist. Return JSON.
... etc ...
```

The template's first comment block IS the slot contract — it's the only docs needed. Renderer fails fast if a required slot is missing.

### CLI command for rendering

```
bmad render <template-name> --story <id> [--stage <s>] [--attempt N] [--mode <m>]
  → stdout: rendered prompt text
  → exit 0 on success; exit 2 if required slot missing; exit 3 if template unknown
```

The orchestrator's per-iteration body is:

```text
For each parallel slot:
  1. bash: bmad render stage_<s> --story <id> --attempt <n> --mode <m> > /tmp/p-<id>.txt
  2. Task({ subagent_type: <l3-for-stage>, prompt: $(cat /tmp/p-<id>.txt) })
Wait for all returns.
For each return: bash: bmad story set-status / bmad dispatch ... (records tokens)
```

The orchestrator's own session-resident prompt is now thin: just "render templates, dispatch, collect results, update state via CLI." All policy lives in `orchestrator_loop.tmpl` and the per-stage templates, NOT in the agent's persistent context.

### Hard persona rules (rendered into every orchestrator iteration)

These live in `orchestrator_loop.tmpl`:

- NEVER do story code work yourself (dispatch only)
- NEVER skip env teardown (try/finally per story)
- NEVER advance past a story without `bmad story set-status`
- NEVER dispatch a story whose deps aren't satisfied (CLI validates)
- ALWAYS use bmad CLI for state mutations (no inline sqlite3 / jq)
- ALWAYS honor retry budgets per stage; mark blocked + continue on exhaustion
- ALWAYS render via `bmad render` (no inline prompt composition)

### Inputs (CLI-level, persisted in `config` table)

- `mode` — `pragmatic | strict`
- `max_parallel` — from `bmad system-check` × user cap
- `pr_strategy` — `per-story | batch | end-only`
- `batch_size` (when pr_strategy=batch; default 3)
- `max_tdd_cycles` (default 3)
- `max_qa_cycles` (default 3)
- `max_ci_retries` (default 2)
- `max_review_iterations` (default 3 strict; 1 pragmatic)
- `checkpoint.count_threshold` (default 4)
- `env.stale_threshold_minutes` (default 120)

### Autonomous loop (rendered into `orchestrator_loop.tmpl`)

```text
Loop:
  1. CLI: bmad system-check --reserve {{.ReserveRamMb}}
     → { free_ram_mb, max_safe_parallel }
  2. effective_parallel = MIN(max_parallel_config, max_safe_parallel, mode_cap)
     where mode_cap = 4 pragmatic, 2 strict (drift tolerance)
  3. CLI: bmad story next --max-parallel <effective_parallel>
     → batch = [{ story_id, stage, env_required }, ...]
  4. If batch empty:
     a. CLI: bmad sprint status → query checkpoints table for unresolved trigger
        - If unresolved checkpoint exists → render checkpoint_summary; HALT for user
     b. If all done → render final PR strategy + exit
     c. Otherwise → break (sprint exhausted)
  5. PARALLEL DISPATCH (single message, N Task calls):
     For each batch item:
       a. If story has no env AND stage != hydrate:
          CLI: bmad env up <story_id> → env_config (persisted in env_allocations)
       b. bash: bmad render stage_<stage> --story <id> --attempt <n> --mode <m>
       c. Task({ subagent_type: <l3-for-stage>, prompt: $rendered })
  6. Wait for ALL N returns
  7. For each return:
     - CLI: bmad dispatch record <id> <stage> <status> --tokens <i:o:cr:cc> --duration <ms>
       (writes dispatches row per §12.7)
     - If status=ok: CLI: bmad story set-status <id> <next_stage_or_complete>
     - If status=blocked:
       - CLI: bmad story set-status increments retry_counts
       - If budget exhausted: CLI: bmad story set-status <id> blocked; CLI: bmad env down <id>
       - Else: same stage, attempt_no++
     - If status=done AND env present: CLI: bmad env down <id>; CLI: bmad story complete <id>
  8. CLI: bmad story checkpoint <id> evaluates §12.5 dual trigger; if fired → goto 4a next loop
  9. Goto Loop
```

### Mode-driven dispatch (rendered into per-stage templates)

**Pragmatic** stages per story: `hydrate → implement → code-review → commit`
**Strict** stages per story: `hydrate → atdd → implement → test-automate → test-review → code-review → commit`
  - STRICT: code-review iterates till clean (`max_review_iterations`); pragmatic = 1 round

### Try/finally infra lifecycle

For each story, regardless of success/failure:

1. CLI: `bmad env up <story>` → env_config (allocates ports, brings up Docker, writes env_allocations row)
2. `<dispatch work via L3 agents>`
3. CLI: `bmad env down <story>` — ALWAYS, even on failure (marks env_allocations.reclaimed_at)
4. CLI: `bmad worktree destroy <story>` — after PR opened (or on user-confirm)

Exception handler at orchestrator level: on crash/interrupt, the orchestrator iterates active stories from SQLite + runs `bmad env down` for each. On next sprint resume, `bmad env cleanup-orphans` runs the activity-based sweep (§12.6) to catch anything the crash missed.

### Checkpoint behavior (dual trigger per §12.5)

After EACH story completes, `bmad story checkpoint <id>` evaluates both triggers and may fire:

- **Count trigger**: `(stories_since_last_checkpoint >= config.checkpoint.count_threshold)`
- **Complexity trigger**: `(just-completed story.complexity == 'high')`

Either trigger writes a `checkpoints` row with `trigger_kind` and `user_decision = NULL`. The orchestrator's next loop iteration (step 4a) detects the unresolved row, renders `checkpoint_summary.tmpl`, HALTs and emits the summary to the user. User runs `bmad sprint resume` (continue) or `bmad sprint pause` (adjust/halt), which writes `user_decision + decided_at`.

### Failure handling per stage

| Stage                | On blocked                                | Retry budget                 | Exhausted action                                |
| -------------------- | ----------------------------------------- | ---------------------------- | ----------------------------------------------- |
| hydrate              | `bmad gate write --concerns ...`          | 1 (deterministic)            | Mark blocked; env not yet up so no teardown     |
| atdd / test-automate | `bmad gate write --concerns ...`          | 1                            | Mark blocked; `bmad env down`                   |
| implement            | `bmad gate write --concerns ...`          | `max_tdd_cycles` (3)         | Mark blocked; `bmad env down`                   |
| test-review          | `bmad gate write --concerns ...`          | re-dispatch implement (`max_qa_cycles`) | Mark blocked; `bmad env down`        |
| code-review          | If iterate: re-dispatch implement         | `max_review_iterations`      | Mark blocked; `bmad env down`                   |
| commit / CI          | If `task check` fails: re-engage implement| `max_ci_retries`             | Mark blocked; `bmad env down`                   |

### Output (return JSON to user at sprint end or HALT)

```json
{
  "mode": "pragmatic",
  "total_stories": 180,
  "completed": ["1.1", "1.2"],
  "blocked": [{ "story_id": "4.7", "reason": "tdd budget exhausted" }],
  "in_progress": [],
  "checkpoint_reached": { "id": 12, "trigger_kind": "complexity", "trigger_detail": "complexity:9.3" },
  "duration_minutes": 240,
  "total_tokens": { "input": 2400000, "output": 380000, "cache_read": 6100000, "cache_create": 450000 }
}
```

Token rollup is queried from `dispatches` table at emit time (per §12.7).

## 5. Composable skills

Two sprint-level skills (`sprint-planning`, `story-checkpoint`) plus four SRP-split infra skills replacing the proposed monolithic `test-env-isolation` (per §12.4): `port-pool`, `docker-up`, `healthcheck`, `sweeper`. The four infra skills compose under `bmad env <verb>` in `application/env/`; none knows about another.

### `sprint-planning` skill

**Purpose:** Read `epics.md` → build dependency graph → produce sprint plan (ordered batches written to `batches` + `batch_stories` tables).

```markdown
---
name: sprint-planning
description: "Builds a sprint dependency graph + ordered batch plan from epics.md. Honors per-story depends_on + file-overlap + parallel cap. Persists to SQLite batches table. Use when starting a new sprint or planning a re-batch after blockers."
tools: Bash, Read
---

# Sprint Planning

INPUT: path to epics.md + max_parallel + per-repo file-overlap conventions

PROTOCOL:
1. Parse epics.md → extract stories + frontmatter (depends_on, affects, resource_budget, requires_android, complexity)
2. CLI: bmad story status (load existing story rows; idempotent — re-planning preserves IDs)
3. Build directed dependency graph from depends_on (read from story_dependencies table)
4. Topo-sort respecting deps
5. Within each topo level: group by NON-overlapping file sets (use story_affects table)
6. Apply parallel cap per batch
7. Mobile serialization: stories with requires_android = 1 get their own batch (or share only with non-android stories)
8. Resource budget sanity-check per batch — sum ram_mb + cpu_cores; warn if exceeds system_check max
9. Persist via CLI: bmad sprint plan --assign-groups <N> (writes batches + batch_stories rows)
10. Return plan JSON: { batches: [[story_ids], ...], total_stories, estimated_duration }
```

### `story-checkpoint` skill (dual-trigger per §12.5)

**Purpose:** Mid-sprint review on either count OR complexity trigger. Summarizes since-last-checkpoint stories, assesses drift, presents continue/adjust/halt options.

```markdown
---
name: story-checkpoint
description: "Mid-sprint review fired by §12.5 dual trigger (count threshold OR completed story with complexity=high). Summarizes drift, presents continue/adjust/halt options. Invoked via `bmad story checkpoint <story-id>` evaluation step."
tools: Bash, Read
---

# Story Checkpoint

INPUT (required):
- checkpoint_id     — INTEGER from checkpoints table (unresolved row to render summary for)
- checkpoint_reason — "count" | "complexity:<story-id>"  (passed in for foregrounding the right detail)

PROTOCOL:
1. CLI: bmad sprint status (overall snapshot)
2. CLI: bmad story status --since-last-checkpoint (list completed + blocked since checkpoint_id-1)
3. For each story in that set:
   - Pull commit_hash + pr_url + retry_counts from SQLite
   - Compute drift signals:
       * commit message subject vs story title: similarity score
       * files modified (git show --stat) vs story_affects rows: overlap ratio
       * downstream-dependency impact: did this story modify files in any other story's affects list?
4. Compose summary:
   - reason = checkpoint_reason  (foreground "complexity:9.3 just landed" or "4 stories since last checkpoint")
   - completed_count, blocked_count
   - drift_signals: [{ story_id, signal, severity }, ...]
   - tokens spent since last checkpoint (SUM from dispatches)
   - PRs opened / merged
5. HALT — emit to user (Markdown), wait for response: `continue` | `adjust` | `halt`
6. User response → CLI: bmad sprint resume (continue) OR bmad sprint pause (adjust/halt)
   (CLI writes checkpoints.user_decision + decided_at)
```

---

### `port-pool` skill (infra split — §12.4)

**Purpose:** Allocate / release port blocks from `.bmad-test-env.yml` `port_range`. Persists to `env_allocations` table. Knows nothing about Docker.

```markdown
---
name: port-pool
description: "Allocate and release port blocks for per-story test envs. Persists allocations in SQLite env_allocations table. Reads port_range + ports_per_story from .bmad-test-env.yml. Cross-project."
tools: Bash, Read
---

# Port Pool

PROTOCOL (allocate):
1. Read .bmad-test-env.yml → port_range, ports_per_story
2. CLI: bmad env status --raw → list of in-use ports (from env_allocations where reclaimed_at IS NULL)
3. Find next free contiguous block of size ports_per_story within port_range
4. CLI: bmad env up --reserve <story_id> --ports <p1,p2,...> (writes env_allocations row; status=reserved)
5. Return { pg_port, redis_port, otel_port, db_name }

PROTOCOL (release):
1. CLI: bmad env down --release <story_id> --reason <completed|stale|manual>
   (sets env_allocations.reclaimed_at + reclaim_reason)
2. Return success
```

### `docker-up` skill (infra split — §12.4)

**Purpose:** Render `.env.test` from allocated ports + `.bmad-test-env.yml`. Run `docker-compose up -d` with labels.

```markdown
---
name: docker-up
description: "Render .env.test from port-pool allocation + .bmad-test-env.yml; bring up docker-compose with [bmad-test-env]-<story> labels. Knows nothing about port allocation or healthchecks."
tools: Bash, Read, Write
---

# Docker Up

INPUT: story_id + env_config (from port-pool) + path to docker-compose.test.yml

PROTOCOL:
1. Read .bmad-test-env.yml → services + image set + resource_limits
2. Write .env.test in worktree:
     PG_PORT=<pg_port>
     REDIS_PORT=<redis_port>
     OTEL_PORT=<otel_port>
     DB_NAME=story_<story_id_underscored>
3. Bash: docker-compose -f docker-compose.test.yml --env-file .env.test up -d
   --label bmad-test-env=<story_id>
4. Capture container_ids from `docker-compose ps -q`
5. CLI: bmad env up --record-containers <story_id> --ids <c1,c2,c3>
   (writes env_allocations.container_ids)
6. Return { container_ids }
```

### `healthcheck` skill (infra split — §12.4)

**Purpose:** Poll service healthchecks until ready or timeout. Per-service timeouts from `.bmad-test-env.yml`.

```markdown
---
name: healthcheck
description: "Poll healthchecks for each service from .bmad-test-env.yml. Per-service timeout. Returns when all green, or fails with which service(s) timed out. Knows nothing about Docker lifecycle."
tools: Bash, Read
---

# Healthcheck

INPUT: story_id + env_config + path to .bmad-test-env.yml

PROTOCOL:
1. Read .bmad-test-env.yml → services[].healthcheck + per-service timeout
2. For each service in parallel:
   - Run healthcheck.test command (e.g., `pg_isready -h localhost -p <pg_port>`)
   - Poll every 1s until success or per-service timeout
3. Aggregate:
   - All green → return ok
   - One+ timed out → return failed_services: [{ name, timeout_s, last_error }]
4. CLI: bmad env up --mark-healthy <story_id>  (only on success)
```

### `sweeper` skill (infra split — §12.4 / §12.6)

**Purpose:** Activity-based stale-env detection. Probes worktree filesystem + git + dispatch return docs; tears down stale envs.

```markdown
---
name: sweeper
description: "Activity-based stale-env sweeper per §12.6. Probes worktree mtimes + git activity + dispatch return docs. Tears down envs with no activity beyond config.env.stale_threshold_minutes. Knows nothing about port allocation or healthchecks."
tools: Bash, Read
---

# Sweeper

INPUT: optional --threshold-minutes <N> override (else read from config table)

PROTOCOL:
1. CLI: bmad env status --raw → list active envs (env_allocations.reclaimed_at IS NULL)
2. CLI: bmad config get env.stale_threshold_minutes → threshold (default 120)
3. For each active env:
   a. Activity probe (any ONE counts as "alive"):
      - find <worktree_path> -type f -newermt "<now - threshold>m" | head -1
      - git -C <worktree_path> log --since "<threshold>m ago" --oneline | head -1
      - SELECT MAX(returned_at) FROM dispatches WHERE story_id=? AND returned_at > <now - threshold>m
   b. If NO activity within threshold → stale
4. For each stale:
   - Bash: docker-compose -f docker-compose.test.yml --env-file <worktree>/.env.test down -v
   - CLI: bmad env down --release <story_id> --reason stale
5. Return: { swept: [<story_id>], kept: [<story_id>] }
```

### Composition

`bmad env up <story>` is implemented in `application/env/up.go` and orchestrates:

```text
port-pool.allocate(story) → docker-up.bringUp(story, env_config) → healthcheck.poll(story, env_config)
```

with try/rollback semantics — if `docker-up` fails, releases port-pool allocation; if `healthcheck` fails, runs `docker-up.tearDown` + releases port-pool. Each skill is independently testable; the composition logic is one focused file.

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

Three layers of configuration, each with one source of truth:

| Layer                  | Source of truth                | Format                       | Mutable via                  |
| ---------------------- | ------------------------------ | ---------------------------- | ---------------------------- |
| Runtime knobs          | `config` table (SQLite)        | key/value rows               | `bmad config <key> <value>`  |
| Test-env topology      | `.bmad-test-env.yml` (per repo)| YAML (see §3)                | hand-edit, committed         |
| Per-story metadata     | `epics.md` story frontmatter   | YAML inside markdown         | hand-edit, committed         |

### Runtime config keys (`config` table)

Seeded by `bmad init` with defaults; mutable via `bmad config <key> [<value>]`.

| Key                                | Default     | Range / Notes                                         |
| ---------------------------------- | ----------- | ----------------------------------------------------- |
| `docs_folder`                      | (required)  | absolute path; set on init                            |
| `mode`                             | `pragmatic` | `pragmatic` \| `strict`                               |
| `max_parallel`                     | `4`         | hard cap; effective cap = min(this, system-check)     |
| `reserve_ram_mb`                   | `8000`      | RAM held back from parallel budget for user dev work  |
| `pr_strategy`                      | `per-story` | `per-story` \| `batch` \| `end-only`                  |
| `batch_size`                       | `3`         | only used when `pr_strategy = batch`                  |
| `max_tdd_cycles`                   | `3`         | retry budget for `implement` stage                    |
| `max_qa_cycles`                    | `3`         | re-dispatch budget triggered by `test-review`         |
| `max_ci_retries`                   | `2`         | retry budget for `commit / CI` stage                  |
| `max_review_iterations`            | `3` strict / `1` pragmatic | code-review iterate-till-clean budget |
| `checkpoint.count_threshold`       | `4`         | §12.5 count trigger — stories since last checkpoint   |
| `env.stale_threshold_minutes`      | `120`       | §12.6 activity-based stale detection window           |

Read examples:

```bash
bmad config mode                              # → pragmatic
bmad config max_parallel                      # → 4
bmad config checkpoint.count_threshold        # → 4
bmad config env.stale_threshold_minutes       # → 120
```

Write examples:

```bash
bmad config mode strict
bmad config max_parallel 2
bmad config checkpoint.count_threshold 6
bmad config env.stale_threshold_minutes 60    # tighten for short workloads
```

### Test-env topology

See §3 `.bmad-test-env.yml` schema (per-repo, hand-edited, committed). Per-stack default budgets there; per-story override via story frontmatter `resource_budget` below.

### Per-story frontmatter (in `epics.md` story sections)

```yaml
---
story_id: "4.1"
depends_on: ["3.1", "3.2"]   # other story IDs (or empty); written to story_dependencies
affects:                      # files this story touches; written to story_affects
  - src/contexts/identity/
resource_budget:              # overrides default_resource_budget_by_stack from .bmad-test-env.yml
  ram_mb: 800
  cpu_cores: 0.6
requires_android: false       # per #338 mobile flag
complexity: medium            # low | medium | high  — §12.5 high-complexity fires checkpoint trigger
---
```

`bmad init` and `bmad sprint plan` parse this frontmatter and write to `stories`, `story_dependencies`, `story_affects` tables. Story-level fields override per-repo defaults; missing fields fall back to defaults from `.bmad-test-env.yml`.

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

## 11. Open questions — status as of 2026-05-16 pressure-test

1. **CLI command set size** — RESOLVED → §12.1
2. **State file format** — RESOLVED → §12.2
3. **Orchestrator agent system prompt size** — RESOLVED → §12.3
4. **Test-env-isolation skill granularity** — RESOLVED → §12.4
5. **Checkpoint cadence** — RESOLVED → §12.5
6. **Failure modes / stale containers** — RESOLVED → §12.6
7. **Mobile lifecycle** — DEFERRED (see #338; no V6 design constraint identified)
8. **Cross-project portability** — DEFERRED (revisit after second project adopts V6)
9. **Migration cost for V4 users** — DEFERRED (mid-build; `bmad migrate` MVP first, hand-holding doc if friction surfaces)
10. **Agent-teams future** — DEFERRED (no integration design needed pre-MVP)
11. **CI integration** — DEFERRED (local-developer for MVP; headless pattern designed only if CI demand materializes)
12. **Cost / observability — per-story token tracking** — RESOLVED → §12.7

Original question framing preserved in git history (commit seeding `docs/spec-v6.md`).

## 12. Resolved decisions (2026-05-16 pressure-test)

### 12.1 CLI command set — verb-namespaced, SRP-tight (~28 commands)

**Decision**: collapse the 37-command surface into verb-namespaced groups where the noun is a coherent SRP unit. Keep root-level commands only for cross-cutting actions.

**Concrete shape**:

- `bmad env <up|down|cleanup-orphans|status>` (was 4 root commands)
- `bmad worktree <create|destroy|list|prune>` (was 3 root commands)
- `bmad depguard <flip|status|history>` (was 2 root commands)
- `bmad story <hydrate|status|checkpoint|next>` (collapses scattered story verbs)
- `bmad sprint <plan|run|pause|resume|status>` (collapses scattered sprint verbs)
- Root-level cross-cutters: `bmad init`, `bmad migrate` (V4→V6), `bmad system-check`, `bmad config <key> <value>`, `bmad dispatch <stage> <story-id>` (manual L3 invocation)

**Target count**: high-20s after collapse, not 37. Each verb-namespace is one Go package under `cmd/`.

**Why**: SRP at the command level mirrors SRP at the package level. A flat 37-command surface is a smell — it implies the cmd/ tree has 37 files with no thematic grouping. Verb-namespacing creates 5-6 cohesive sub-packages, each with a single responsibility (env management, worktree lifecycle, depguard ratcheting, story unit, sprint orchestration).

**Refactor pressure tolerated**: if a namespace's command list grows past ~5 verbs, that's a signal the namespace itself is doing too much — split it.

### 12.2 State store — SQLite

**Decision**: SQLite, NOT JSON. Migrate V4's `bmad-progress.json` semantics into `bmad-state.db` with schema versioning.

**Why**: parallel-N orchestration is the V6 differentiator. With 4 stories executing concurrently and each L3 stage updating state (hydrated → atdd → impl → reviewed → committed), JSON read-modify-write races are inevitable. SQLite gives ACID + WAL + cheap query for "what's blocked," "what's in-flight," "per-story token spend," "drift since last checkpoint."

**Implications for `infrastructure/state/`**:

- New `infrastructure/state/sqlite/` adapter; `infrastructure/state/json/` (V4) kept only for the `bmad migrate` one-shot import path
- Domain interface: `domain/state/Store` — write-once interface; both adapters satisfy it
- Schema lives in `infrastructure/state/sqlite/schema/` as numbered `.sql` files (golang-migrate style — reuse the nutrition-v2-go convention)
- Initial schema: `stories`, `batches`, `worktrees`, `env_allocations`, `dispatches` (one row per L3 stage invocation, captures tokens + duration + status), `checkpoints`, `depguard_flips`

**Concurrency model**: WAL mode by default; one writer per process. The orchestrator agent process and CLI-invoked sub-commands serialize through file lock; sub-commands that only read use a read-only connection.

### 12.3 Orchestrator system prompt — template + render pipeline

**Decision**: replace the monolithic system-prompt sketch in §4 with a **template + render** pipeline. CLI loads a structured template (text/template or XML), fills variables from current state, passes the rendered prompt to the agent at dispatch time.

**Architecture**:

- `infrastructure/prompts/templates/` — versioned template files, one per agent role (orchestrator, dispatcher-stage-N, checkpoint-summary, etc.) and per significant variant
- Templates expose named slots: `{{.StoryId}}`, `{{.Mode}}`, `{{.HydratedFilePath}}`, `{{.PriorStageOutput}}`, `{{.SprintState}}`, etc.
- Hard persona rules + try/finally infra lifecycle + mode dispatch table live in the template, NOT in the agent's session memory
- Per-dispatch CLI step: load template → resolve variables from SQLite + filesystem → render → pass to agent as system prompt or first user message
- Templates can be enriched at dispatch with extra context (current epic doc excerpt, failing test output from prior attempt, etc.) by passing additional named context blocks

**Why**: a single monolithic orchestrator prompt at the size sketched in §4 risks context-budget overflow across long sessions. Templating lets each stage carry only what it needs. It also keeps prompts version-controlled, diffable, and reviewable — the same hygiene we apply to code.

**Template format**: pick during build. Default-bet on Go `text/template` (zero deps, native variable substitution, supports conditionals + ranges for context blocks). XML files are equally viable if structured tooling around them helps — defer to whoever implements the prompts package.

**SRP boundary**: `infrastructure/prompts/` owns load + render. `application/dispatch/` owns "what template + what context for this stage." Neither knows about the other's internals.

### 12.4 Test-env-isolation — split per concern (SRP)

**Decision**: split the proposed `test-env-isolation` skill into per-concern skills. Each is independently invocable, independently testable, independently versioned.

**Split**:

- `port-pool` — allocate/release port blocks from a configured range; persists allocations in SQLite (`env_allocations` table)
- `docker-up` — render `.env.test` from allocated ports + `.bmad-test-env.yml` + bring up `docker-compose -f docker-compose.test.yml --env-file .env.test up -d`
- `healthcheck` — poll service healthchecks with timeout per service from `.bmad-test-env.yml`
- `sweeper` — query Docker for `[bmad-test-env]-*` labels; cross-reference SQLite `env_allocations` + worktree activity probe; tear down stale environments

**Why**: each concern has its own failure modes, retry semantics, and observability needs. A fat skill couples them and makes "the docker-up step is flaky" indistinguishable from "the healthcheck timeout is wrong." Split, each skill is one diagnosable unit.

**Composition**: `bmad env up <story>` orchestrates the four sequentially with try/finally cleanup. The composition lives in `application/env/` — the skills themselves know nothing about each other.

### 12.5 Checkpoint cadence — both triggers (count + complexity)

**Decision**: checkpoint fires on EITHER trigger:

- **Count trigger**: every N stories (default 4; configurable via `bmad config checkpoint.count_threshold`)
- **Complexity trigger**: after any story tagged `complexity: high` in its frontmatter (e.g., meal-prep state machine, LP solver, GraphQL→Huma cutover)

Whichever fires first.

**Why**: a flat count misses the moment a heavy slice lands and drift is most likely. A complexity-only trigger misses the slow accumulation of small drifts across N simple stories. Both keep the orchestrator honest at both granularities.

**Implementation**: `story-checkpoint` skill consumes a `checkpoint_reason` parameter (`count` | `complexity:<story-id>`) so the summary it produces can foreground the right detail.

### 12.6 Stale-environment detection — activity-based, not timestamp-based

**Decision**: CLI tracks port allocations + worktree activity. Stale-environment detection is **activity-based**, not pure age-based.

**Activity probe** (per worktree, run on sweeper invocation or `bmad env status`):

- File mtime of any file inside the worktree changed in the last threshold window?
- Git activity (commits, staged changes, branch updates) within window?
- Most recent L3 dispatch return document timestamp within window?

**Stale threshold**: configurable; default proposed as 2-3 hours but tune downward (maybe 30-60 minutes) once we observe real workloads. Live in `bmad-state.db` config table; `bmad config env.stale_threshold_minutes <N>` adjusts.

**Auto-teardown**: stale env → `sweeper` skill brings it down + releases ports + marks `env_allocations` row as `reclaimed`.

**Auto-respawn on resume**: when work resumes on a story whose env was reclaimed, the orchestrator detects the missing env via state-store lookup and re-runs `bmad env up <story>` transparently. No user prompt unless the original `env_config` had non-default settings worth confirming.

**Why**: pure age-based sweep would kill an env mid-debugging-session if the user stepped away briefly. Activity-based sweep matches the actual signal — "no one is touching this" — and the resume path keeps the user's workflow invisible.

### 12.7 Token cost tracking — per dispatch, in SQLite

**Decision**: every L3 dispatch records token cost in the `dispatches` table (input_tokens, output_tokens, cache_read_tokens, cache_create_tokens, model, duration_ms). `bmad story status <id>` + `bmad sprint status` surface running totals.

**Why**: enables cost fine-tuning during V6 maturation — which agents run hot, which stories blow budget, whether STRICT mode is worth the spend. Cheaper to collect from day one than retrofit.

**Source**: Claude Code emits this metadata in tool/agent result payloads. The dispatcher reads it from the agent's return JSON and persists.

## 13. Cascading rewrites — COMPLETED 2026-05-17

All five cascade rewrites from the §12 pressure-test landed as separate commits on v2:

- ✅ **§2 (CLI command set)** — verb-namespaced, 32 commands — commit `738c886`
- ✅ **§3 (State schema)** — full SQLite DDL (12 tables) — commit `6e20ced`
- ✅ **§4 (Orchestrator)** — template + render pipeline — commit `0d50af2`
- ✅ **§5 (Composable skills)** — test-env-isolation split into 4 SRP skills (port-pool / docker-up / healthcheck / sweeper); story-checkpoint dual-trigger contract — commit `fe76e8b`
- ✅ **§7 (Config schema)** — three-layer model + new keys (`checkpoint.count_threshold`, `env.stale_threshold_minutes`, story `complexity`) — commit `b0addcf`

Build phase can start against this spec.

## Spec sign-off

- 2026-05-16 — original spec captured (seed commit `b606673`)
- 2026-05-16 — §12 pressure-test decisions recorded (Q1-Q6 + Q12 resolved; Q7-Q11 deferred) — commit `932b107`
- 2026-05-17 — §13 cascade rewrites landed (§2, §3, §4, §5, §7 fully restructured to §12 decisions)
- **Status: ready for build.** Pick a §13 area and start the corresponding Go package: `infrastructure/state/sqlite/` (most foundational), then `infrastructure/prompts/`, then `cmd/` namespaces (one per §2 namespace).
