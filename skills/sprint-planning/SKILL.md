---
name: sprint-planning
description: Parse an `epics.md` file into stories + dependency edges + parallel-eligible batches via `bmad sprint plan`. Use whenever you're starting a new sprint, re-batching after blockers shift, or ingesting a fresh epic into an existing state. Trigger this any time the user mentions "plan the sprint", "ingest the epics", "rebuild batches", or simply hands you an `epics.md` and asks "what's next" — the planner does dependency-respecting topo sort + file-overlap-aware grouping + android-serialization, all in one shot, and the result is what every subsequent `bmad story next` reads from.
---

# sprint-planning

You're turning a human-authored `epics.md` into machine-readable state:
stories rows, dependency edges, affects sets, and the ordered batch queue
that `bmad story next` will draw from.

## When this matters

Without explicit batching, the orchestrator picks stories greedily — first
unblocked, first served. That works until you have 200 stories with
overlapping `affects:` paths, when greedy picks 4 stories that all touch
`src/identity/` and they serialize on filesystem conflicts. The planner
runs a topo sort + within-layer file-overlap check + android serialization
once, persistence everything, and the orchestrator never has to re-derive.

## Inputs

- `epics.md` — markdown with stories formatted as:

  ```markdown
  ### Story 4.1: Identity Aggregates

  ---
  story_id: "4.1"
  depends_on: ["3.1", "3.2"]
  affects:
    - src/identity/
  resource_budget: { ram_mb: 800, cpu_cores: 0.6 }
  requires_android: false
  complexity: high
  ---

  (free-form body)
  ```

  Stories without frontmatter still get ingested as stub rows (the
  markdown header carries the id/title); they just won't participate in
  dependency or overlap detection until you add frontmatter.

- (optional) `max_parallel` override — else reads from `config.max_parallel`.

## Protocol

### Plan

```bash
bmad sprint plan [--epics <path-to-epics.md>] [--max-parallel N]
```

If `--epics` is omitted, defaults to `<docs_folder>/epics.md` from config.

Output (JSON):

```json
{
  "stories_inserted": 12,
  "stories_updated":  3,
  "dependencies_added": 8,
  "affects_added": 17,
  "batches_created": 5,
  "batch_ids": [1, 2, 3, 4, 5]
}
```

### Verify what landed

```bash
bmad sprint status            # human-readable summary
bmad story status             # full story list
```

### Re-plan after editing epics.md

`bmad sprint plan` is idempotent on stories (existing rows are Update()d,
runtime status / commit_hash / ci_passed / completed_at are preserved).
Batches are cleared and rebuilt — so if you re-plan mid-sprint, the
orchestrator just picks up the new batch queue on its next loop iteration.

## Batching rules (what the planner actually does)

1. **Topo-sort by `depends_on`** — Kahn's algorithm; ties broken by
   alphabetical story_id for deterministic output.
2. **Within each topo level**, group greedily into batches respecting:
   - `max_parallel` cap (config or override)
   - file-overlap: no two stories with intersecting `affects:` paths in
     the same batch (so they don't serialize on filesystem conflicts)
   - android serialization: any story with `requires_android: true` gets
     a solo batch (mobile emulator can't be parallel-shared)
3. **Persist** batches to `batches` + `batch_stories` tables. The orchestrator's
   `bmad story next` reads from these.

## Failure modes

- `parse epics ...: yaml parse: ...` — broken YAML inside a story's
  frontmatter. The error message points at the bad story; fix the YAML
  and re-run.
- `planner insert ...: FOREIGN KEY constraint failed` — a `depends_on`
  references a story_id that doesn't exist in this epics.md OR in any
  prior state. Either add it to the epics file or remove the dependency.
- `0 batches created` — every story has a circular dependency (none
  unblocked). Topo sort can't proceed. Inspect `depends_on` chains for
  cycles.

## Why this skill exists

Sprint planning is a once-per-sprint computation that constrains every
subsequent `bmad story next` call. Doing it eagerly + persisting means the
runtime hot path is `SELECT next_batch FROM batches WHERE status = 'planned'
ORDER BY sequence_no LIMIT 1` — fast, deterministic, replayable. Lazy
planning (recompute on every `next` call) would re-parse epics + re-topo
on every iteration and silently drift if epics.md changed mid-sprint.
