---
name: docker-up
description: Render `.env.test` from a port-pool allocation and bring up the per-story `docker-compose.test.yml` infrastructure. Use after `port-pool` has allocated ports and BEFORE running healthcheck or any integration tests. Trigger this whenever an agent is about to run integration tests against postgres/redis/otel inside a worktree — even if the user just says "spin up the test infra" or "start the containers" — because the env_config must be rendered through this skill, not hand-typed, to stay consistent with the port-pool's allocation.
---

# docker-up

You're rendering `.env.test` in a worktree and running `docker compose up -d`
against the project's `docker-compose.test.yml`. This skill assumes the
port-pool has already allocated and returned an `env_config` JSON.

## When this matters

Hand-typing port numbers into `.env.test` is how mismatches happen — agent A
allocates `7600` for story X, agent B writes `5432` into the env file, story
X's tests bind to the wrong port and silently pass against an unrelated
database. This skill keeps `.env.test` strictly derived from the port-pool's
output.

## Inputs (from port-pool)

```json
{
  "story_id": "4.1",
  "pg_port": 7600,
  "redis_port": 7601,
  "otel_port": 7602,
  "db_name": "story_4_1"
}
```

Plus:

- `<worktree_path>` — directory where `.env.test` will be written
  (usually returned by the `worktree create` skill).
- `docker-compose.test.yml` — must exist at the repo root (project-owned).

## Protocol

### Render `.env.test`

Write `<worktree_path>/.env.test` with EXACTLY these keys (whatever the
port-pool returned — do not edit):

```text
PG_PORT=<pg_port>
REDIS_PORT=<redis_port>
OTEL_PORT=<otel_port>
DB_NAME=<db_name>
```

If `otel_port` is missing from the env_config, omit the `OTEL_PORT=` line —
don't substitute a default.

### Bring containers up

```bash
cd <worktree_path>
docker compose -f docker-compose.test.yml --env-file .env.test up -d \
  --label bmad-test-env=<story_id>
```

The `bmad-test-env=<story_id>` label is what the `sweeper` skill uses to
identify these containers later. Do not skip it.

### Record container IDs back to state

After bring-up succeeds, capture the container IDs and record them so
`bmad env status` reflects reality and the sweeper can find them by label:

```bash
ids=$(docker compose -f docker-compose.test.yml ps -q | tr '\n' ',')
bmad env up <story_id> --record-containers --ids "$ids"   # (planned in M11)
```

(For now until that CLI flag lands, the labels alone are enough for the
sweeper to find containers; the IDs are nice-to-have audit data.)

### Hand off to healthcheck

After bring-up, the containers are running but not necessarily ready. Pass
control to the `healthcheck` skill with the same env_config. Do NOT assume
"docker compose up exit code 0" means the services accept connections.

## Failure modes

- `docker: Cannot connect to the Docker daemon` — Docker isn't running.
  Surface this to the user; don't retry blindly. They need to start their
  Docker engine.
- `port is already allocated` — something not tracked by bmad is using the
  port. Run `lsof -i :<port>` (or equivalent) to find the squatter. The
  port-pool only knows about its own allocations; external processes are
  invisible to it.
- `Cannot find docker-compose.test.yml` — the project hasn't set up a v6
  test infra file yet. Don't create one — that's a project-level decision.
  Surface to the user and stop.

## Why this skill exists

Splitting docker-up from port-pool and healthcheck (SRP per spec §12.4)
makes each layer independently diagnosable. When tests fail, you can ask
"was the port allocated?" (port-pool's job), "did docker bring it up?"
(this skill), and "did the service become ready?" (healthcheck's job) as
three separate questions, instead of one mushy "the env is broken."
