---
name: port-pool
description: Reserve a contiguous port block for one story's isolated test environment via `bmad env up <story-id>`. Use whenever a parallel-running story needs its own Postgres / Redis / OTel ports and you don't want collisions with other in-flight stories. Trigger this any time you're about to bring up Docker for a per-story test env, even if the user just says "start the test env" or "give me ports for story X" — the port-pool is the source-of-truth for what's free.
---

# port-pool

You're allocating an isolated port block for ONE story's test infrastructure.
The bmad CLI owns the allocation table (`env_allocations` in `bmad-state.db`)
so concurrent agents never hand out overlapping ports.

## When this matters

When N stories run in parallel, each needs its own Postgres + Redis (+ optional
OTel) ports. Without a central allocator, two stories pick `:5432` and the
second one fails to bring up its DB. This skill makes the allocator central.

## Protocol

### Allocate

```bash
bmad env up <story-id> [--config-dir <dir>]
```

- `<story-id>` is the canonical id from `stories` table (e.g., `4.1`).
- `--config-dir` defaults to `.` — the directory containing `.bmad-test-env.yml`.
  Defaults inside that file: `port_range: {start: 7600, end: 7799}`,
  `ports_per_story: 10`.

Output (JSON on stdout):

```json
{
  "story_id": "4.1",
  "pg_port": 7600,
  "redis_port": 7601,
  "otel_port": 7602,
  "db_name": "story_4_1"
}
```

Pass this whole object to the `docker-up` skill — it renders the
`.env.test` and brings up the compose file.

### Release

Always release after the story finishes or fails:

```bash
bmad env down <story-id> --reason completed   # or stale / manual
```

This frees the port block so the next story can reuse it. The DB record is
NOT deleted — it's marked reclaimed with a timestamp + reason, so you can
audit "what envs ran when" later.

### Check what's in use

```bash
bmad env status         # human table
bmad env status --raw   # json array of every active allocation
```

## Failure modes

- `port pool: no free block of size N in range [start, end]` — every block
  in the configured range is already reserved. Either someone forgot to
  release, or your range is too small. Run `bmad env cleanup-orphans` first;
  if that doesn't help, widen `port_range` in `.bmad-test-env.yml`.
- `port pool: ports_per_story must be >= 2` — config file has a too-small
  block size. Postgres + Redis need at least 2 slots; OTel makes 3.
- `FOREIGN KEY constraint failed` — the story_id doesn't exist in the
  `stories` table yet. Run `bmad sprint plan` (which ingests stories from
  epics.md) before allocating envs.

## Why this skill exists

Splitting port-pool from docker-up keeps responsibilities tight (SRP per
spec §12.4). The port-pool answers "which ports are free?" — independent
of whether Docker is healthy, whether the compose file is correct, whether
healthchecks pass. When something breaks, you know which layer to look at.
