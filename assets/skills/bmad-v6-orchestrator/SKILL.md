---
name: bmad-v6-orchestrator
description: |
  Drive BMad v6 story execution end-to-end for the nutrition-v2-go EDA cutover.
  The main session adopts the L1 orchestrator role: queries `bmad` CLI for
  state + next-actions, dispatches L3 stage subagents (story-hydrator,
  atdd-writer, tdd-implementer, test-automate, test-reviewer, code-reviewer)
  via the Task tool, runs per-story env up/down via try/finally, manages
  retry budgets + checkpoints, and surfaces decisions to the human at
  defined gates. Use this skill whenever the user says "run story", "execute
  story", "run the sprint", "execute sprint", "story status", "next actions",
  or any phrase that involves dispatching the v6 story-runner pipeline.
  Also use proactively whenever the user is in a develop-active worktree
  and clearly wants to advance an EDA cutover slice — even if they don't
  invoke the skill by name.
metadata:
  type: skill
  surface: orchestration
  project: nutrition-v2-go
---

# BMad v6 Orchestrator (L1 driver)

The main session, when this skill is loaded, IS the v6 L1 orchestrator. The
job is to drive a long-running story-execution loop by querying state from
the `bmad` CLI, dispatching L3 worker subagents in parallel batches, and
managing per-story test-env lifecycle without losing track of which story
is in which stage.

This is **reactive** orchestration — sequence emerges from CLI state +
subagent returns, not from a hardcoded script.

## Why this skill exists (architectural constraint)

Subagents cannot dispatch other subagents (validated empirically; see
`docs/architecture/eda-cutover/agent-orchestrator-v6-spec.md` §1 +
`p1-validation-2026-05-16`). The orchestrator role HAS to live in the
main session, not as a subagent. That's why the old `.claude/agents/story-runner.md`
and `.claude/agents/sprint-runner.md` were deleted (commit `65c67e6b`) and
why this knowledge lives as a skill instead.

If a task seems to require "spawn an orchestrator subagent which then
spawns N L3 workers," **don't**. Run the orchestration in this session
directly; dispatch L3 workers via Task (single-hop only).

## The three layers

```
L1 = THIS Claude session, with this skill loaded
       │
       ├── Bash → bmad CLI (~/go/bin/bmad)
       │           = the state machine + dispatch-prompt emitter ("L2")
       │
       └── Task → .claude/agents/{story-hydrator, tdd-implementer,
                  code-reviewer, atdd-writer, test-automate, test-reviewer}
                  = L3 worker subagents (one-hop dispatch only)
```

L1 reads CLI output, dispatches L3 workers, pipes their JSON returns back
into the CLI's state machine. Repeat until the sprint is done or a
checkpoint fires.

## Reference: full v6 spec

This skill distills the v6 protocol. The canonical, authoritative source is
`docs/architecture/eda-cutover/agent-orchestrator-v6-spec.md` (633 lines).
Read it on first activation if you haven't already, especially:

- §4 — full autonomous loop with all retry/budget edge cases
- §5 — composable skills (sprint-planning, story-checkpoint, etc.)
- §6 — the L3 agent inventory and per-agent return contracts

The spec covers cases this skill body doesn't — when in doubt, defer to it.

## Helper-skill invocation contract (load-bearing)

Seven helper skills ship alongside this orchestrator. Each one wraps a raw
`bmad <cmd>` with smart pre/post behavior (port-collision avoidance,
activity-based reclamation, drift detection, etc.). **The orchestrator
MUST invoke the matching skill at each lifecycle point below instead of
calling `bmad` directly.** The skills internally call the same CLI verb —
the contract with bmad-cli is unchanged — but skipping the wrapper loses
the smart behavior.

| Lifecycle point                              | Skill(s) to invoke                                                | Replaces raw call              |
| -------------------------------------------- | ----------------------------------------------------------------- | ------------------------------ |
| Story-start env-up (every story)             | `port-pool` → `docker-up` → `healthcheck` (chained, in this order) | `bmad env up <story>`          |
| Post-story-completion (every story)          | `context-propagation` → `story-checkpoint`                         | `bmad story checkpoint <id>`   |
| Pre-loop sprint plan refresh (stale state)   | `sprint-planning`                                                  | `bmad sprint plan`             |
| Session-exit or orphaned-env detection       | `sweeper`                                                          | `bmad env cleanup-orphans`     |

Invocation form (Claude Code's Skill tool): `Invoke skill: <slug>` then wait
for its return before invoking the next one in the chain. Skills are
side-effect-aware and emit their own JSON envelopes; the orchestrator reads
those envelopes the same way it reads `bmad` envelopes today.

When a section below says e.g. *"Invoke skill: port-pool"*, that is a
literal instruction — do not substitute a raw `bmad env up` call.

## Per-story sequence (the simplest invocation)

When the user says **"run story <id>"** or **"execute story <id> [mode]"**:

1. **Confirm state.** `bmad story status <id>` — note the current stage.
2. **Bring up env (try/finally start) via the helper-skill chain.**
   - **Invoke skill: `port-pool`** — reserves a contiguous port block for
     this story so we never collide with another in-flight worktree.
   - **Invoke skill: `docker-up`** — renders `.env.test` from the port
     allocation and runs the per-story `docker-compose.test.yml`.
   - **Invoke skill: `healthcheck`** — polls each service (postgres, redis,
     otel) until it accepts connections, or fails with which service(s)
     timed out. Never run integration tests before this returns success —
     `docker compose up -d` exiting 0 means containers started, NOT that
     services are ready.
   - Capture the resulting `env_config` (port, db url, redis url, etc.)
     from the chain's combined output for L3 prompts. The skills together
     replace what was previously a raw `bmad env up <id>` call.
3. **Pre-extract context.** `bmad story context-bundle <id>` — produces
   `_bmad-output/context-bundles/<id>.md`.
4. **Hydrate.** `bmad story hydrate <id>` — bmad emits the hydrator
   dispatch prompt to stdout.
5. **Dispatch L3 story-hydrator.** Use Task tool, `subagent_type: story-hydrator`,
   `prompt: <bmad's emitted prompt + env_config + idempotency key>`.
6. **Read return JSON.** If `status: ok`, pipe back to bmad:
   `bmad story set-status <id> hydrated`. If `status: blocked` or `errored`,
   handle per the retry policy in §"Retry budgets" below.
7. **Get next stages.** `bmad story applicable-stages <id>` returns ordered list
   based on story type + mode (e.g. for `feature` story in pragmatic mode:
   `[tdd-implement, code-review, commit]`).
8. **Loop through remaining stages.** For each stage:
   - Dispatch the matching L3 agent via Task.
   - On `ok` return, advance state.
   - On `blocked`, apply retry policy.
9. **Mark complete.** `bmad story complete <id> --commit` (or `--pr` for
   per-story PR strategy).
10. **Post-completion: propagation + checkpoint via helper skills.**
    - **Invoke skill: `context-propagation`** — scans the just-completed
      story's outputs (commit, PR, files changed) and surfaces signals
      about which downstream stories' hydrated specs may need re-rendering.
      Skipping this lets late-stage drift accumulate silently.
    - **Invoke skill: `story-checkpoint`** — evaluates the §12.5 dual-trigger
      checkpoint (count threshold OR high-complexity story). If it fires,
      HALT for user input per §"Checkpoint behavior" below.
11. **Teardown env (try/finally end).** `bmad env down <id>` —
    ALWAYS, including on failure paths.

The bmad CLI knows what stage comes next based on story_type + mode + current
state. Don't hardcode the sequence; ask the CLI.

## Sprint mode (autonomous loop)

When the user says **"run the sprint"** or **"execute sprint [mode]"**:

```
Pre-loop (once per session):
  0a. If the state DB has never been planned, OR `bmad sprint status`
      reports stale batches:
        → Invoke skill: sprint-planning
          (parses epics.md → stories + dependency edges + parallel-eligible
          batches; without this, `bmad next-actions` returns nothing useful)
  0b. Optional safety pass before dispatching anything:
        → Invoke skill: sweeper
          (tears down test envs with no recent activity — guards against
          leftover containers from a crashed prior session)

Loop:
  1. bmad system-check --reserve <reserve_ram>
       → { free_ram_mb, max_safe_parallel }
  2. effective_parallel = min(config_max, max_safe_parallel, mode_cap)
       mode_cap = 4 pragmatic, 2 strict
  3. bmad next-actions --max-parallel <effective_parallel>
       → batch = [{ story_id, action, env_config? }, ...]
  4. If batch empty:
       - checkpoint-reached → Invoke skill: story-checkpoint → halt for user
       - all-done → final PR strategy + exit
       - otherwise → break (sprint exhausted)
  5. PARALLEL DISPATCH — one message, N Task calls:
       For each batch item:
         a. If story has no env yet AND action != hydrate:
              → Invoke skill: port-pool   (reserve port block)
              → Invoke skill: docker-up   (render .env.test + bring up infra)
              → Invoke skill: healthcheck (poll services until ready)
              env_config = chain output
         b. Task({ subagent_type: <agent>, prompt: <protocol + env_config> })
  6. Wait for ALL N returns.
  7. For each return:
       - ok → bmad set-status <id> <next_stage_or_complete>
       - blocked → retry budget logic (see §"Retry budgets")
       - story complete:
           → Invoke skill: context-propagation  (scan outputs for downstream drift)
           → Invoke skill: story-checkpoint     (§12.5 dual-trigger evaluation)
           → bmad env down <id>; bmad set-complete <id>
  8. Goto Loop.

Session-exit (or detected orphaned envs at any point):
  → Invoke skill: sweeper
    (activity-based stale-env teardown — won't kill an env you're actively
    debugging; will reclaim everything from a crashed orchestrator)
```

Important: dispatch N L3 workers **in a single message with N Task tool
calls**. That's how Claude Code runs them in parallel. Separate messages =
sequential. The helper-skill chain at step 5a is per-story sequential
(port-pool must finish before docker-up has ports to bind), but the
**across-stories** parallelism at step 5b is preserved.

## Mode-driven dispatch

Use the user's stated mode (default `pragmatic`):

- **Pragmatic** stages per `feature` story:
  `hydrate → tdd-implement → code-review → commit`
- **Strict** stages per `feature` story:
  `hydrate → atdd → tdd-implement → test-automate → test-review → code-review → commit`
  - code-review iterates till clean (`max-review-iterations`, default 3)

Other story types (`docs-only`, `architectural-decision`, etc.) have their
own stage matrices. Don't hardcode — `bmad story applicable-stages <id>`
returns the right ordered list.

## Retry budgets per stage

| Stage                | Budget                                          | Exhausted action                            |
| -------------------- | ----------------------------------------------- | ------------------------------------------- |
| hydrate              | 1 (deterministic)                               | Mark blocked. env not up yet — no teardown. |
| atdd / test-automate | 1                                               | Mark blocked. env down.                     |
| tdd-implement        | `max-tdd-cycles` (default 3)                    | Mark blocked. env down.                     |
| test-review          | re-dispatch tdd-implement, `max-qa-cycles`      | Mark blocked. env down.                     |
| code-review          | `max-review-iterations` (strict 3, pragmatic 1) | Mark blocked. env down.                     |
| commit / CI          | `max-ci-retries` (default 2)                    | Mark blocked. env down.                     |

On each `blocked` return: `bmad add-concerns <id>` to record the L3 agent's
reason, then advance counter OR mark blocked when budget exhausted.

## Try/finally infra lifecycle

For every story, regardless of success or failure:

```
1. Invoke skill: port-pool      → reserved port block
2. Invoke skill: docker-up      → infra brought up against allocated ports
3. Invoke skill: healthcheck    → services confirmed ready (postgres/redis/otel)
4. <do work via L3 dispatches>
5. Invoke skill: context-propagation  (on completion — surface drift signals)
6. Invoke skill: story-checkpoint     (on completion — §12.5 dual-trigger)
7. bmad env down <story>        — ALWAYS, including on failure paths
8. bmad worktree destroy <story>  — after PR opened (or on user-confirm)
```

If this session crashes or the user interrupts:
- Restart, then **Invoke skill: `sweeper`** — it's the activity-based reaper
  that will tear down any env whose orchestrator died without leaving an
  env you're still touching at risk. Pure `bmad env list --status running`
  + manual `bmad env down` is acceptable but loses the activity heuristic.
- Don't leak Docker containers.

## Checkpoint behavior

After every `checkpoint-after-stories` stories complete (default 4) OR
whenever a high-complexity story just finished (§12.5 dual-trigger):

1. `bmad status` — overview
2. **Invoke skill: `context-propagation`** — scans the just-completed story's
   outputs (commit, PR, files changed) and surfaces signals about which
   downstream stories' hydrated specs might need re-rendering. Run BEFORE
   `story-checkpoint` so any drift it surfaces makes it into the checkpoint
   summary.
3. **Invoke skill: `story-checkpoint`** — produces summary + drift
   assessment for the last N stories. The skill itself wraps
   `bmad story checkpoint <id>` and fires only when the dual-trigger
   condition is met, so it's safe to invoke after every completion.
4. If the checkpoint fired: **HALT** — surface summary + ask:
   `continue | adjust | halt`
5. User responds → `bmad sprint-resume` (continue) or `bmad sprint-pause`
   (exit cleanly)

The user is the authority on whether drift is acceptable; you don't decide.

## Citation freshness (cost-critical rule)

**Never forward stale file:line citations between stages.** Empirical
data from the first two stories (1.1, 1.2; combined ~704k subagent
tokens) showed that ~50% of Story 1.1's cost came from code-review
iteration loops where iter-2 broke iter-1's fixes because the L1
prompt carried iter-1's line numbers — which were no longer accurate
after the implementer's iter-2 changes.

When dispatching a later-stage L3 agent that needs to reference earlier
work, choose ONE of these patterns (in preference order):

1. **Pass concerns, not coordinates.** Record findings from earlier
   stages via `bmad story add-concerns <id> --file <stage>-concerns.json`
   and tell the next L3 agent to read concerns from bmad-state. The
   concerns include intent ("missing input validation on userID") but
   not file:line that may have shifted.

2. **Pass git refs, not paths.** If you must point at code, point at
   the commit SHA + path. The L3 agent reads via `git show <sha>:<path>`
   which gives them a stable snapshot regardless of subsequent edits.

3. **Re-derive citations at dispatch time.** If a fresh symbol lookup
   is cheap (`atlas codebase find` is sub-100ms in v0.1.1+), call it
   right before constructing the prompt instead of caching from an
   earlier stage.

4. **Pass intent, let the L3 agent discover.** "Review the implementer's
   most recent commits since the last review iteration" — the L3 has
   Read + Bash and can `git log` / `git diff` for itself.

**Hard rule: never embed a `file.go:42` from iter-N's return into
iter-(N+1)'s dispatch prompt.** The implementer may have moved that line.
Pass `git diff iter-N..HEAD` or "see concerns from prior review" instead.

This is the single biggest token-cost lever in the orchestrator. The
2026-05-18 baseline (Story 1.1, 3 code-review iters, 152k tokens) is
the watermark to beat; expect ~30% reduction once this rule is honored.

## Telemetry — record the full token breakdown per dispatch

Empirical finding (Epic 1 + Epic 2 batches 1-2, ~5.69M tokens, ~36
dispatches): **Claude Code's Task tool result only exposes `total_tokens`
to the L1 orchestrator. The cache fields (`cache_read_input_tokens`,
`cache_creation_input_tokens`) are NOT surfaced as structured fields.**
That's why the early dispatches recorded 0% cache hit rate — the
orchestrator had no way to populate the cache flags.

**Resolution (bmad-cli PR #36):** every L3 stage template now instructs
the agent to emit a TOKEN_BREAKDOWN line as the very last line of its
text response. The L1 orchestrator parses that line and uses the values.

### Parse the TOKEN_BREAKDOWN line from EVERY L3 dispatch return

Anchor the regex:

```
^TOKEN_BREAKDOWN: input=(\d+) output=(\d+) cache_read=(\d+) cache_create=(\d+)\s*$
```

Apply to the L3 agent's text response (the Task tool result body), NOT
to any `<usage>` block — Claude Code's tool-result usage block only has
`total_tokens`. The agent writes the breakdown line as ordinary prose so
the orchestrator can extract it.

### Then record all four fields\*\*Parse all four and record them via the

bmad-cli flags\*\* added in PR #18:

```bash
bmad dispatch record \
  --story-id <id> \
  --stage <stage> \
  --input-tokens <input_tokens> \
  --output-tokens <output_tokens> \
  --cache-read-tokens <cache_read_input_tokens> \
  --cache-create-tokens <cache_creation_input_tokens>
```

`--total-tokens` is no longer required — bmad-cli computes the sum.

**Why this matters:** with the breakdown, `bmad sprint status` surfaces
`cache_hit_rate = cache_read / (input + cache_read)`. The Epic 1
baseline showed 0% cache hits across ~1M input tokens — without the
breakdown we can't diagnose why. With the breakdown we can see whether
the issue is per-dispatch prefix variance (e.g., variable content at
the prompt prefix, busting cache) vs per-iteration drift vs system
prompt churn.

**Hard rule:** every L3 return MUST be followed by a `bmad dispatch
record` call with all four breakdown fields. If the `<usage>` block is
malformed or missing a field, log a warning + record what's available
(use 0 for missing fields rather than guess). The accounting must NEVER
be silently zero across the board.

## Surfacing decisions to the user

Stop and surface (don't auto-proceed) at these moments:

- **After hydrate**: show the hydrated file path + brief diff stats; ask
  "proceed to implement?"
- **After tdd-implement**: show net LOC added + test count; ask "ready for
  review?"
- **After code-review**: if iteration-mode and reviewer found findings,
  surface count + severity; ask "iterate or accept and continue?"
- **On any L3 `blocked` / `errored` return**: STOP immediately. Show the
  agent's reason. Ask for guidance — don't paper over.
- **At checkpoint cadence**: per §"Checkpoint behavior" above.

For pragmatic single-story runs against the user's first 2-3 stories, you
MAY collapse the post-stage prompts into one "story done, here's the
summary" message at the end. For sprint mode, surface per-checkpoint.

## Output JSON when sprint completes

```json
{
  "mode": "pragmatic",
  "total_stories": 180,
  "completed": ["1.1", "1.2", "..."],
  "blocked": [{ "story_id": "4.7", "reason": "..." }],
  "in_progress": [],
  "checkpoint_reached": false,
  "duration_minutes": 240
}
```

For single-story runs, return:

```json
{
  "story_id": "1.1",
  "status": "complete|blocked",
  "stages_run": ["hydrate", "tdd-implement", "code-review", "commit"],
  "duration_minutes": 18,
  "concerns": []
}
```

## Hard persona rules

- **Never do story code work yourself.** No impl, no test-writing, no
  review prose. Dispatch L3 agents — they own the work.
- **Never skip env teardown.** try/finally is invariant. If you crash, the
  next session must clean up dangling envs.
- **Never advance state without updating the CLI.** `bmad set-status`
  every transition.
- **Never dispatch a story whose deps aren't satisfied.** The CLI validates;
  if `bmad next-actions` doesn't return a story, don't try to force it.
- **Always one-hop dispatch.** L1 → L3 via Task. Never L1 → subagent → L3
  (the L2 layer is bmad-cli, not a Claude subagent).

## Project context (nutrition-v2-go specifics)

- Repo root: `/home/alejandrososa/Documents/startup-projects/nutrition-v2-go`
- Active dev worktree: `.worktrees/develop-active/` (branch: `develop`)
- Backend port for this worktree: **7602** (see memory
  `project_worktree_port_allocations`). Pass `PORT=7602` when bringing up
  the backend. Never default to `:7777` — it collides with concurrent
  sessions on other worktrees (see memory `feedback_session_isolation_port`).
- Commit scopes are restricted by commitlint — prefer
  `feat(business-logic)`, `chore(web)`, `fix(api-client)`, etc. The L3
  commit subagent handles this; don't override.
- Atlas state DB defaults to per-worktree `.atlas/atlas.db`. If you observe
  multiple worktrees re-indexing redundantly, suggest the user set
  `ATLAS_DB_PATH="$HOME/.atlas/nutrition-v2-go.db"` in their shell rc.
  Don't auto-set it.
- Pre-cutover bmad-cli verbs live under `bmad story` / `bmad sprint` /
  `bmad env` / `bmad system` / `bmad worktree`. Check `bmad --help` for the
  current surface — the v6 spec describes intended commands but the
  installed binary is the truth.

## Quick smoke

When the skill activates for the first time in a session, run:

```bash
which bmad && bmad --version
which atlas && atlas --version
ls .claude/agents/
ls .claude/skills/
```

Confirm:

- bmad CLI present, atlas v0.1.1+ present
- All 6 L3 agents (`story-hydrator`, `atdd-writer`, `tdd-implementer`,
  `test-automate`, `test-reviewer`, `code-reviewer`) exist under
  `.claude/agents/`
- All 7 helper skills referenced above (`port-pool`, `docker-up`,
  `healthcheck`, `context-propagation`, `story-checkpoint`,
  `sprint-planning`, `sweeper`) exist under `.claude/skills/`. If any are
  missing, suggest the user run `bmad install` to drop the embedded set
  into the project's `.claude/` directory.

If anything's missing, surface before attempting orchestration.

## Out of scope

- **Code work in any story.** That's L3's job.
- **Architectural decisions** about which slice to attempt next outside
  of what `bmad next-actions` returns. The user planned the sprint; you
  execute it.
- **Cutover ops** (atlas re-index, registry migration, etc.) — separate
  from story dispatch.
- **Cross-repo work** like opening PRs in atlas or bmad-cli — the commit
  L3 subagent handles per-story commits + PRs in nutrition only.

## If you're not sure

Defer to the v6 spec at
`docs/architecture/eda-cutover/agent-orchestrator-v6-spec.md`. If it's
silent, ask the user — don't invent. The spec was validated against the
2 hydrated test stories (1.1, 1.2); deviating without explicit user
approval risks producing dispatches the CLI's state machine can't
reconcile.
