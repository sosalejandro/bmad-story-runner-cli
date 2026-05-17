---
name: sweeper
description: Find and tear down test envs that have had no activity (no dispatches, no worktree edits) for longer than the configured threshold (default 120 min) via `bmad env cleanup-orphans`. Use whenever you suspect leftover infrastructure from a crashed orchestrator session, or as a periodic cleanup pass before starting a fresh sprint. Trigger this any time the user mentions "leftover containers", "stale environments", "free up ports", or simply "clean up" in the context of a bmad workspace — the sweeper is the safe answer because it's activity-based (will NOT kill an env you're actively debugging).
---

# sweeper

You're running the activity-based stale-env sweep (spec §12.6). The skill is
"safe by default" — it inspects per-env activity signals before tearing
anything down, so an env you stepped away from for 5 minutes won't get
killed, but one whose orchestrator crashed 3 hours ago will.

## When this matters

The orchestrator manages env lifecycle via try/finally. But if the
orchestrator crashes mid-batch (or someone kills the Claude session),
env_allocations rows are left with `reclaimed_at IS NULL` and Docker
containers keep running. Pure age-based sweeps (kill anything > 4h old) are
too aggressive — they'd kill envs you're actively debugging. Pure
manual cleanup is too risky — users forget. This skill threads the needle by
asking "has anything actually touched this env recently?"

## Activity signals (any one = "alive")

The bmad CLI checks two signals; either one keeps the env alive:

1. **Dispatch recency** — any `dispatches` row with `returned_at` newer than
   the threshold? If yes, the L3 agent is actively working this story.
2. **Worktree filesystem mtime** — any file under `worktrees.path` modified
   newer than the threshold? Skips `.git/` and other hidden dirs to avoid
   git-housekeeping false positives.

If both probes return "no activity", the env is considered stale.

## Protocol

### Standard sweep (use config threshold)

```bash
bmad env cleanup-orphans
```

Reads `config.env.stale_threshold_minutes` (default 120) and probes every
active env. Returns JSON:

```json
{
  "swept": ["4.1", "4.7"],
  "kept":  ["4.2", "4.3"]
}
```

For each swept env, the CLI marks `env_allocations.reclaimed_at = now()` and
`reclaim_reason = "stale"`. It does **NOT** run `docker compose down` —
that's the caller's job because it requires the worktree path + compose file.

### After sweep — tear down Docker

For each story in `swept`, run:

```bash
cd <worktree_path>
docker compose -f docker-compose.test.yml --env-file .env.test down -v
```

Or use the label filter directly:

```bash
docker ps -q --filter "label=bmad-test-env=<story_id>" | xargs -r docker stop
docker ps -aq --filter "label=bmad-test-env=<story_id>" | xargs -r docker rm
```

### Custom threshold

For a more aggressive sweep (e.g., before a fresh sprint where you know
nothing should be in flight):

```bash
bmad env cleanup-orphans --threshold-minutes 15
```

For a more conservative one (e.g., when you've been deep in a debug session):

```bash
bmad config env.stale_threshold_minutes 360   # persist for future sweeps
```

## Failure modes

- `swept` is unexpectedly large — your threshold is too tight, or you have
  many genuinely-abandoned envs from prior crashes. Widen the threshold or
  inspect `bmad env status` to see what's there before sweeping.
- `swept` is empty when you expected results — your worktree probe is
  hitting recent git activity (git commits update file mtimes). This is
  expected and intentional — git activity IS activity. If you genuinely
  want to nuke everything, use `bmad env down <story-id> --reason manual`
  per env.
- `worktree probe takes a long time` — large worktrees with many files
  slow the walk. Acceptable trade-off; the alternative (no probe) would be
  unsafe.

## Why this skill exists

Activity-based detection (per spec §12.6) means the sweeper can be safely
re-run by anyone, any time, without coordination. It does the right thing
based on what's actually happening, not on a wall-clock timer that doesn't
know about your debug session. This is the layer of "everything that
shouldn't be running, isn't" — it lets the orchestrator's try/finally
deal with the happy path and leaves cleanup-after-crash to a separate,
idempotent, safe operation.
