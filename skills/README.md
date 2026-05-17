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

| Skill              | Layer        | CLI command(s) it wraps                                       |
| ------------------ | ------------ | ------------------------------------------------------------- |
| `port-pool`        | infra (SRP)  | `bmad env up <id>` + `bmad env down <id>`                     |
| `docker-up`        | infra (SRP)  | renders `.env.test` + runs `docker compose ... up -d`         |
| `healthcheck`      | infra (SRP)  | `pg_isready`, `redis-cli ping`, `curl` polls                  |
| `sweeper`          | infra (SRP)  | `bmad env cleanup-orphans` + targeted `docker compose down`   |
| `sprint-planning`  | sprint-level | `bmad sprint plan`                                            |
| `story-checkpoint` | sprint-level | `bmad story checkpoint <id>`                                  |

The four infra skills are the §12.4 SRP split of the proposed
`test-env-isolation` skill — each owns one concern, composes via the
orchestrator (or higher-level `bmad env up`).

The two sprint-level skills wrap the planner + dual-trigger checkpoint
logic that the orchestrator agent invokes on every loop iteration.

## How they compose

```
sprint-planning
     ↓ (writes batches + stories)
[orchestrator loop]
     ↓ (picks story from batch)
worktree-create (planned)
     ↓ (writes worktree row, suggests git cmd)
port-pool ──→ docker-up ──→ healthcheck
     ↓            ↓             ↓
  env_config   .env.test   "ready"
     ↓
[L3 agent dispatch — hydrate, implement, etc.]
     ↓ (on each completion)
story-checkpoint ──→ (HALT if fired)
     ↓ (else continue)
[next story]
     ↓ (periodically)
sweeper ──→ tear down stale envs
```

## Why thin wrappers (not Go re-implementations)

The CLI is the system of record. Skills are agent-facing instructions.
Putting business logic in skills (e.g., re-implementing the port-pool
allocator in agent prose) means two sources of truth that drift apart.
Putting it in the CLI + having skills call the CLI means the SQLite store
is always authoritative and the agent's job is just "translate user
intent → CLI invocation."

See `docs/spec-v6.md` §5 and §12.4 for the full design.
