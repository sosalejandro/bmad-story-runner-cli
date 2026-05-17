---
name: context-propagation
description: After a story completes, scan its outputs (commit, PR, files changed) and surface signals about which downstream stories' hydrated specs might need re-rendering. Use IMMEDIATELY after every `bmad story complete` (between the completion and dispatching the next story). Trigger this even when the user says "just keep going" or the story looks small — late-stage drift in long sprints is exactly what cheap, regular propagation checks prevent. Without this step, downstream stories get dispatched against stale assumptions (e.g., a doc Story 1.1 produced gets referenced by Story 1.3, but only the orchestrator notices that 1.1's actual output diverged from what 1.3's hydrated spec assumed).
---

# context-propagation

You're running after a story completes, BEFORE picking up the next one.
Goal: surface "downstream stories may need re-hydration" signals to the
orchestrator, but DON'T make the re-hydrate decision automatically — that's
the user's call (or an `--auto-rehydrate` flag they explicitly turn on).

## When this matters

A 180-story sprint is a Telephone Game. Story 1.1 picks `measurements` as
the canonical reference BC. Story 1.3 writes a walkthrough doc that says
"using `measurements` BC". Story 4.2 implements the canonical pattern
"per the walkthrough in 1.3". If 1.1's actual implementation chose
`routines` instead at the last minute (because measurements turned out
flakier than the audit suggested), Stories 1.3 and 4.2 dispatch with
stale assumptions. By the time you notice at Story 4.2's review, you've
wasted 3 stories' worth of work.

This skill catches drift one story later, not 5.

## When to invoke

EVERY TIME after a `bmad story complete` returns successfully. Insert it
between completion and the next `bmad story next --claim`. Cost is small
(a few SQL queries + a grep); benefit compounds with sprint length.

## Inputs

- `--just-completed <story-id>` (required) — the story to scan downstream from.

## Protocol

### Step 1: Pull the just-completed story's blast radius

```bash
# What did the story touch?
affects=$(bmad story status <just-completed> --raw 2>/dev/null | jq -r '.affects[]?')
files_changed=$(git -C <worktree-path> diff --name-only main..HEAD 2>/dev/null)
commit_msg=$(git -C <worktree-path> log -1 --format=%s)
```

If the worktree is already pruned by this point, fall back to the PR diff
via `gh pr view <pr-url> --json files`.

### Step 2: Find downstream candidate stories

A downstream story is "worth checking" if ANY one of these holds:
- It explicitly depends_on the just-completed story
- It's in the same epic (same id prefix — e.g., `1.x` for Epic 1)
- Its `affects` set intersects with the just-completed story's
- Its hydrated spec file (if present) textually references the just-completed
  story's id, key files, or canonical-decision keywords

```bash
deps=$(bmad story status --raw 2>&1 | jq -r --arg id <just-completed> '
  .[] | select(.depends_on // [] | index($id)) | .story_id')

same_epic=$(bmad story status --raw 2>&1 | jq -r --arg prefix "<just-completed-prefix>" '
  .[] | select(.story_id | startswith($prefix)) | .story_id')

# Affects-overlap and grep-of-hydrated-file are best done as a tiny
# helper — keep this skill itself thin and lean on the CLI.
```

### Step 3: For each candidate, classify the signal

For each downstream story Y, produce one of:

- **`re-hydrate`** — Y's hydrated spec references a file the just-completed
  story renamed, deleted, or significantly restructured. Re-running
  `bmad story hydrate <Y> --re-hydrate` is recommended.
- **`review`** — Y's hydrated spec references a concept the just-completed
  story affected (e.g., names a BC that was renamed), but it's not clear
  whether the reference is structurally broken. Ask the user.
- **`none`** — no detectable impact. Most candidates land here.

### Step 4: Emit the signal report

```json
{
  "just_completed": "<story-id>",
  "downstream_check": [
    {
      "story_id": "1.3",
      "signal": "re-hydrate",
      "reason": "Hydrated spec at _bmad/stories/1.3.md references 'measurements BC' which Story 1.1 renamed to 'routines BC' in the canonical-reference doc."
    },
    {
      "story_id": "1.2",
      "signal": "review",
      "reason": "Same epic; Story 1.1's commit message mentions 'changed canonical-service shape'. Story 1.2 implements that shape — confirm signatures still match."
    }
  ],
  "auto_actions_taken": []
}
```

`auto_actions_taken` is reserved for future use when an `--auto-rehydrate`
flag is added. Today this skill ONLY surfaces signals — it does not mutate
any hydrated specs.

### Step 5: Hand off

- If `downstream_check` is empty → orchestrator continues to next story
  with no interruption.
- If non-empty → emit the report to the orchestrator's stdout. The
  orchestrator either:
    a. Acts on `re-hydrate` signals automatically if --auto-rehydrate
       is set in config (future)
    b. Surfaces the report to the user and asks "re-hydrate Y? continue
       without changes? halt sprint to manually adjust?"
- Either way, store the report in the latest checkpoint's summary_json
  if a checkpoint is also being fired — gives the user a single artifact
  to review.

## What this skill DOES NOT do

- ❌ Modify any hydrated spec file or stories row — surface-only.
- ❌ Re-dispatch the L3 story-hydrator agent on its own — that's the
  orchestrator's job after seeing the report.
- ❌ Block the next dispatch — emits report and returns; orchestrator
  decides whether to halt.
- ❌ Catch every kind of drift — heuristics here are file-based + grep-based.
  Semantic drift ("we said milestone X means Y but actually we mean Z")
  needs `story-checkpoint`'s human-review step, not this skill.

## Why this skill exists

Drift is silent and cumulative; manual review is expensive and
inconsistently applied. Putting the check in a skill that runs after
EVERY completion makes propagation a default, not an afterthought. The
cost is one extra round-trip per story (a few seconds); the benefit is
catching drift one story later instead of 5-20.

This is the "fast feedback loop" answer to "what happens when stories
in an epic implicitly depend on each other but the dependency wasn't
declared in `depends_on`."
