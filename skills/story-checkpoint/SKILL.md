---
name: story-checkpoint
description: Evaluate the §12.5 dual-trigger checkpoint after each story completion via `bmad story checkpoint <story-id>`. Fires if either (a) `stories_since_last >= config.checkpoint.count_threshold` (default 4), or (b) the just-completed story has `complexity: high`. Use IMMEDIATELY after every `bmad story complete` invocation — the orchestrator should NOT advance to the next story without consulting this skill, because skipping checkpoints lets drift accumulate silently across long sprints. Trigger this even when the user says "just keep going" — the checkpoint is fast and harmless when it doesn't fire.
---

# story-checkpoint

You're consulting the §12.5 dual-trigger after a story has completed. Either
trigger fires a `checkpoints` row that the orchestrator HALTs on at its next
loop iteration, surfacing a summary to the user for continue/adjust/halt
decision.

## When this matters

180-story sprints accumulate small drifts: a service shape that subtly
differs from the architecture, a saga that quietly stopped emitting an
event, an ISP narrow-port that grew a fat method. Without checkpoints, you
notice on slice 22 instead of slice 5. The dual-trigger catches drift at two
rhythms — every N stories (steady-cadence catch-up) AND after every heavy
story (the moment risk is highest).

## When to invoke

EVERY TIME a story completes — i.e., immediately after each:

```bash
bmad story complete <story-id>
```

(or after `bmad gate write <id> PASS` if you're going through the gate
namespace). Do not skip even for "simple" stories — the count trigger
needs every completion to register accurately.

## Protocol

```bash
bmad story checkpoint <story-id>
```

Where `<story-id>` is the story that JUST completed (the trigger is evaluated
against its complexity + the global stories_since_last counter).

Output (JSON):

```json
{ "fired": false }
```

OR:

```json
{
  "fired": true,
  "trigger_kind": "complexity",
  "trigger_detail": "complexity:4.5",
  "checkpoint_id": 12
}
```

### When `fired: false`

Proceed to the next story. Nothing to surface to the user.

### When `fired: true`

The checkpoint row is now in the database with `user_decision = NULL`.
The orchestrator's autonomous loop will detect this on its next iteration
and HALT, rendering a summary for the user. Do NOT try to advance the
sprint until the user decides — calling `bmad story next` while an
unresolved checkpoint exists is the orchestrator's signal to stop.

To check if a checkpoint is pending without firing a new one:

```bash
bmad sprint status     # shows "UNRESOLVED CHECKPOINT" if pending
```

To resolve and resume:

```bash
bmad sprint resume     # implicit user_decision=continue
bmad sprint pause      # user_decision=halt; sprint pauses for adjustment
```

(Full continue/adjust/halt semantics — currently `bmad sprint resume`
records continue; adjust+halt are reserved for a future flag.)

## Trigger details

The CLI evaluates both triggers and fires the FIRST one that hits:

- **complexity** — `stories.complexity == 'high'` for the just-completed
  story. `trigger_detail` = `complexity:<story-id>`.
- **count** — `(SELECT COUNT(*) FROM stories WHERE status='complete' AND
  completed_at > last_checkpoint_triggered_at) >= config.checkpoint.count_threshold`.
  `trigger_detail` is null.

Default `count_threshold` is 4 — tunable via:

```bash
bmad config checkpoint.count_threshold 6   # less chatty
bmad config checkpoint.count_threshold 2   # more chatty (good for unstable phases)
```

## Failure modes

- `Get story <id>: state: not found` — the story_id passed in doesn't
  exist. Probably a typo or you called this before the story was inserted.
- Checkpoint fires every time even after a clean run — your
  `checkpoint.count_threshold` is too low for the sprint's natural rhythm,
  OR every story happens to be complexity=high. Tune one or the other.
- Checkpoint never fires — `count_threshold` is set higher than total
  stories AND nothing in the epic is `complexity: high`. Either expected
  (small sprint) or a config mistake.

## Why this skill exists

Drift is silent and cumulative. The cheapest insurance against
180-story drift is a structured pause every N stories where the user
gets a chance to course-correct. Putting this in a skill (rather than
hand-coding "every 4 stories, look around") means the orchestrator agent
doesn't have to remember — it just calls this skill after every
completion and lets the dual-trigger decide.
