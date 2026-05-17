# bmad v6 skills

Six markdown skills that document agent-facing protocols for invoking the
bmad CLI. Each is a thin protocol wrapper — the CLI does the work, the
skill tells the agent which `bmad <cmd>` to run and how to interpret the
output.

## Installation

```bash
# system-wide (recommended — one install, every project benefits)
cp -r skills/* ~/.claude/skills/

# OR per-repo
cp -r skills/* <repo>/.claude/skills/
```

After install, Claude Code auto-discovers them at session start.

## Inventory

| Skill                 | Layer        | CLI command(s) it wraps                                       |
| --------------------- | ------------ | ------------------------------------------------------------- |
| `port-pool`           | infra (SRP)  | `bmad env up <id>` + `bmad env down <id>`                     |
| `docker-up`           | infra (SRP)  | renders `.env.test` + runs `docker compose ... up -d`         |
| `healthcheck`         | infra (SRP)  | `pg_isready`, `redis-cli ping`, `curl` polls                  |
| `sweeper`             | infra (SRP)  | `bmad env cleanup-orphans` + targeted `docker compose down`   |
| `sprint-planning`     | sprint-level | `bmad sprint plan`                                            |
| `story-checkpoint`    | sprint-level | `bmad story checkpoint <id>` (dual-trigger §12.5)             |
| `context-propagation` | sprint-level | post-completion downstream-drift scan; surfaces re-hydrate signals |

## Story type → stage applicability (the matrix)

Epics.md frontmatter declares `story_type: doc | code | mixed` (default
`code`). The orchestrator queries `bmad story applicable-stages <id>
--mode <m>` to get the actual list of stages to dispatch — non-applicable
stages auto-skip with a pre-recorded blocked-NA dispatch row.

| story_type / mode | pragmatic                              | strict                                                                  |
| ----------------- | -------------------------------------- | ----------------------------------------------------------------------- |
| code              | hydrate → implement → code-review → commit | hydrate → atdd → implement → automate → test-review → code-review → commit |
| doc               | hydrate → implement → code-review → commit | hydrate → implement → code-review → commit (atdd / automate / test-review skipped) |
| mixed             | hydrate → implement → code-review → commit | hydrate → atdd → implement → automate → test-review → code-review → commit |

Doc stories also skip `env up / env down` — no test infra needed for
markdown-only work. Saves ~30-100k subagent tokens per doc story (atdd
no longer dispatched to discover it's N/A; automate + test-review same).

The four infra skills are the §12.4 SRP split of the proposed
`test-env-isolation` skill — each owns one concern, composes via the
orchestrator (or higher-level `bmad env up`).

The two sprint-level skills wrap the planner + dual-trigger checkpoint
logic that the orchestrator agent invokes on every loop iteration.

## How they compose (rolling-window, per-session)

The recommended orchestrator pattern is **N independent sessions, each
running one story at a time**. Each session loops:

```
sprint-planning  (runs ONCE per sprint, by one session)
     ↓ (writes batches + stories)

[per-session loop — N sessions running this in parallel]
     │
     ├─ bmad story next --max-parallel 1 --claim --claimer <session-id>
     │      ↓ (atomic claim — no two sessions ever pick the same story)
     │
     ├─ port-pool ──→ docker-up ──→ healthcheck
     │      ↓            ↓             ↓
     │   env_config   .env.test   "ready"
     │
     ├─ bmad dispatch begin → idempotency key
     │      ↓ (key flows into the rendered prompt)
     │
     ├─ bmad render stage_<X> --idempotency-key <key>
     │      ↓
     │   [Task() — L3 agent dispatch]
     │      ↓
     ├─ bmad dispatch record --key <key> --status <ok|blocked|errored>
     │      ↓
     ├─ bmad story complete  (releases claim)
     │      ↓
     ├─ context-propagation  ──→ (surface downstream re-hydrate signals)
     │      ↓
     ├─ story-checkpoint     ──→ (HALT if §12.5 dual trigger fires)
     │      ↓
     └─ loop (back to `bmad story next`)

[sweeper — runs periodically, independent of the per-story loop]
     ↓ activity-based detection
     ↓ tear down stale envs that crashed sessions left behind
```

### Why per-session parallelism, not per-call

Claude Code's `Task()` tool blocks until ALL spawned subagents return.
A single session dispatching 4 in parallel sits idle until the slowest
finishes. N independent sessions, each at parallelism=1, maintain
N-way throughput with no batch-barrier waste. The atomic claim
(`bmad story next --claim`) makes this safe — no two sessions ever
work the same story.

See `infrastructure/prompts/templates/orchestrator_loop.tmpl` for the
full per-session script.

## Why thin wrappers (not Go re-implementations)

The CLI is the system of record. Skills are agent-facing instructions.
Putting business logic in skills (e.g., re-implementing the port-pool
allocator in agent prose) means two sources of truth that drift apart.
Putting it in the CLI + having skills call the CLI means the SQLite store
is always authoritative and the agent's job is just "translate user
intent → CLI invocation."

See `docs/spec-v6.md` §5 and §12.4 for the full design.
