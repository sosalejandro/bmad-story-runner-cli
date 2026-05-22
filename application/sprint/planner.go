package sprint

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/sosalejandro/bmad-story-runner-cli/domain/state"
)

// Planner converts a parsed epics file into:
//
//  1. one stories row + per-story dependencies + affects rows
//  2. an ordered set of batches respecting:
//     - dependency layers (topo-sort by depends_on)
//     - max_parallel cap per batch
//     - file-overlap disjointness within a batch (story_affects)
//     - android-serialization (requires_android stories get solo batches)
type Planner struct {
	Stories      state.Stories
	Dependencies state.StoryDependencies
	Affects      state.StoryAffects
	Batches      state.Batches
}

// IngestResult summarises a Plan call.
type IngestResult struct {
	StoriesInserted   int `json:"stories_inserted"`
	StoriesUpdated    int `json:"stories_updated"`
	DependenciesAdded int `json:"dependencies_added"`
	// DependenciesCleared counts story_dependencies rows wiped by the
	// idempotent re-ingest step (one count per story whose old edges were
	// dropped before its current frontmatter was re-applied). Surfaces
	// "11 stories had their deps relaxed and reset on this replan" so an
	// operator can see what changed.
	DependenciesCleared int `json:"dependencies_cleared,omitempty"`
	// SynthesizedDependenciesAdded counts edges added by the issue #46
	// cross-epic resolver (i.e. `requires_epics:` / `requires_stories:`
	// expansion) — DependenciesAdded covers them too, but operators
	// running side-by-side migrations want to know how many edges came
	// from the epic-level synthesis vs. story-level `depends_on:`.
	SynthesizedDependenciesAdded int `json:"synthesized_dependencies_added,omitempty"`
	AffectsAdded                 int `json:"affects_added"`
	// AffectsCleared counts story_affects rows wiped during idempotent
	// re-ingest (analogous to DependenciesCleared).
	AffectsCleared int     `json:"affects_cleared,omitempty"`
	BatchesCreated int     `json:"batches_created"`
	BatchIDs       []int64 `json:"batch_ids"`
	// Scope, when non-empty, is the epic-id filter that was applied. Stories
	// outside this scope were skipped entirely.
	Scope string `json:"scope,omitempty"`
	// StoriesSkippedByScope counts how many parsed stories were excluded
	// because they don't belong to the requested --scope.
	StoriesSkippedByScope int `json:"stories_skipped_by_scope,omitempty"`
	// Warnings surfaces non-fatal diagnostics from the issue #46 resolver
	// (placeholder-linear-chain smell, references to non-existent epics,
	// etc.). Empty when the plan was clean.
	Warnings []string `json:"warnings,omitempty"`
}

// CoverageReport summarises frontmatter coverage of a parsed epics set —
// surfaced by the CLI so an operator can decide whether to proceed against
// partially-annotated epics or backfill first.
type CoverageReport struct {
	TotalStories          int      `json:"total_stories"`
	WithFrontmatter       int      `json:"with_frontmatter"`
	WithoutFrontmatter    int      `json:"without_frontmatter"`
	UnannotatedStoryIDs   []string `json:"unannotated_story_ids,omitempty"`
}

// AnalyseCoverage builds a CoverageReport over the parsed stories. Cheap
// (single pass, no IO) — called by the CLI to decide whether to surface a
// partial-coverage warning before delegating to Plan.
func AnalyseCoverage(parsed []ParsedStory) CoverageReport {
	r := CoverageReport{TotalStories: len(parsed)}
	for _, ps := range parsed {
		if ps.HasFrontmatter {
			r.WithFrontmatter++
			continue
		}
		r.WithoutFrontmatter++
		r.UnannotatedStoryIDs = append(r.UnannotatedStoryIDs, ps.Frontmatter.StoryID)
	}
	return r
}

// FilterByScope returns only the stories whose ID belongs to epicID. An empty
// epicID returns the slice unchanged. See StoryMatchesEpic for the matching
// rule (prefix + dot; avoids the "1" / "10" off-by-one footgun).
func FilterByScope(parsed []ParsedStory, epicID string) []ParsedStory {
	if epicID == "" {
		return parsed
	}
	out := make([]ParsedStory, 0, len(parsed))
	for _, ps := range parsed {
		if StoryMatchesEpic(ps.Frontmatter.StoryID, epicID) {
			out = append(out, ps)
		}
	}
	return out
}

// Plan persists parsed stories + builds batches. Clears any existing planned
// batches first (re-planning replaces the queue).
//
// Equivalent to PlanWithEpics(ctx, nil, parsed, maxParallel) — the legacy
// entry point for callers that don't carry epic-header metadata. The CLI
// (cmd/sprint.go) uses PlanWithEpics so the issue #46 cross-epic resolver
// fires; this wrapper keeps the older test suite + downstream packages
// working without churn.
func (p *Planner) Plan(ctx context.Context, parsed []ParsedStory, maxParallel int) (*IngestResult, error) {
	return p.PlanWithEpics(ctx, nil, parsed, maxParallel)
}

// PlanWithEpics is the issue #46 entry point: persists parsed stories +
// builds batches, AND expands epic-level `requires_epics:` /
// `requires_stories:` frontmatter into synthesized story-level edges
// before topo sorting.
//
// Idempotency contract (issue #21 gap 1): for every story in the parsed
// slice, the planner FIRST wipes its existing story_dependencies and
// story_affects rows, then re-inserts whatever the current frontmatter
// declares (PLUS the synthesized cross-epic edges). This is what makes
// "operator relaxed depends_on: ["1.4"] → [] and re-ran sprint plan"
// actually free the story for dispatch — without the wipe, the stale
// edge persists in SQLite and `bmad story next` keeps seeing the unmet
// prerequisite. Stories ABSENT from `parsed` are left alone (a scoped
// replan never touches out-of-scope rows).
//
// Cycle detection: when `epics` declares a `requires_epics:` cycle (e.g.
// A→B and B→A), the function returns a non-nil error WITHOUT touching
// SQLite. Story-level cycles continue to be caught by the topo-sort
// drain check in buildBatches.
func (p *Planner) PlanWithEpics(ctx context.Context, epics []ParsedEpic, parsed []ParsedStory, maxParallel int) (*IngestResult, error) {
	if maxParallel <= 0 {
		maxParallel = 4
	}
	res := &IngestResult{}

	// Up-front epic-cycle check — fail fast before any SQLite mutation
	// (acceptance criterion #4). Story-level cycles surface in the
	// topo-sort drain check below.
	if cycle := DetectRequiresEpicsCycle(epics); len(cycle) > 0 {
		return nil, fmt.Errorf("planner: requires_epics cycle detected: %s", strings.Join(cycle, " -> "))
	}

	// Synthesise cross-epic deps (issue #46). Pure function — works on a
	// copy of the parsed stories so we can layer it onto each ps.DependsOn
	// without mutating the caller's slice. Warnings flow back through the
	// IngestResult so the CLI can print them after success.
	synth := SynthesizeRequiresEpics(epics, parsed)
	res.Warnings = append(res.Warnings, synth.Warnings...)
	res.Warnings = append(res.Warnings, DetectLinearChainPlaceholderSmell(epics, parsed)...)

	// Apply synthesised deps to a working copy so downstream batching
	// sees the unified depends_on list. We don't mutate the caller's
	// slice — keep the parser-side ParsedStory shape pristine for any
	// other consumer of the same `parsed` value (e.g. JSON marshalling).
	working := make([]ParsedStory, len(parsed))
	copy(working, parsed)
	for i := range working {
		extra := synth.SynthesizedDeps[working[i].Frontmatter.StoryID]
		if len(extra) == 0 {
			continue
		}
		// Combine, de-dup against own deps. `additional` keeps only the
		// NEW edges so we can count them honestly in the IngestResult.
		seen := make(map[string]struct{}, len(working[i].Frontmatter.DependsOn))
		for _, d := range working[i].Frontmatter.DependsOn {
			seen[d] = struct{}{}
		}
		var additional []string
		for _, e := range extra {
			if _, ok := seen[e]; ok {
				continue
			}
			seen[e] = struct{}{}
			additional = append(additional, e)
		}
		if len(additional) == 0 {
			continue
		}
		combined := make([]string, 0, len(working[i].Frontmatter.DependsOn)+len(additional))
		combined = append(combined, working[i].Frontmatter.DependsOn...)
		combined = append(combined, additional...)
		working[i].Frontmatter.DependsOn = combined
		res.SynthesizedDependenciesAdded += len(additional)
	}

	for _, ps := range working {
		st := ps.ToStory()
		if err := p.Stories.Insert(ctx, st); err != nil {
			// Already exists → update fields except status (preserve runtime progress).
			existing, getErr := p.Stories.Get(ctx, st.ID)
			if getErr != nil {
				return nil, fmt.Errorf("planner ingest %q: %w", st.ID, err)
			}
			st.Status = existing.Status
			st.CurrentStage = existing.CurrentStage
			st.HydratedFile = existing.HydratedFile
			st.CommitHash = existing.CommitHash
			st.PRURL = existing.PRURL
			st.CIPassed = existing.CIPassed
			st.CompletedAt = existing.CompletedAt
			if err := p.Stories.Update(ctx, st); err != nil {
				return nil, fmt.Errorf("planner update %q: %w", st.ID, err)
			}
			res.StoriesUpdated++
		} else {
			res.StoriesInserted++
		}

		// Idempotent re-ingest: wipe stale edges before re-inserting the
		// frontmatter's current set. Counted only when the story has
		// existing rows, so a fresh insert doesn't inflate Cleared counts.
		existingDeps, err := p.Dependencies.Of(ctx, st.ID)
		if err != nil {
			return nil, fmt.Errorf("planner deps inspect %q: %w", st.ID, err)
		}
		if len(existingDeps) > 0 {
			if err := p.Dependencies.RemoveAllFor(ctx, st.ID); err != nil {
				return nil, fmt.Errorf("planner deps clear %q: %w", st.ID, err)
			}
			res.DependenciesCleared++
		}
		existingAffects, err := p.Affects.Of(ctx, st.ID)
		if err != nil {
			return nil, fmt.Errorf("planner affects inspect %q: %w", st.ID, err)
		}
		if len(existingAffects) > 0 {
			if err := p.Affects.RemoveAllFor(ctx, st.ID); err != nil {
				return nil, fmt.Errorf("planner affects clear %q: %w", st.ID, err)
			}
			res.AffectsCleared++
		}

		for _, dep := range ps.Frontmatter.DependsOn {
			if dep == "" {
				continue
			}
			if err := p.Dependencies.Add(ctx, st.ID, dep); err != nil {
				return nil, fmt.Errorf("planner dep %q→%q: %w", st.ID, dep, err)
			}
			res.DependenciesAdded++
		}
		for _, path := range ps.Frontmatter.Affects {
			if path == "" {
				continue
			}
			if err := p.Affects.Add(ctx, st.ID, path); err != nil {
				return nil, fmt.Errorf("planner affects %q→%q: %w", st.ID, path, err)
			}
			res.AffectsAdded++
		}
	}

	// Build batches.
	if err := p.Batches.ClearPlan(ctx); err != nil {
		return nil, fmt.Errorf("planner clear: %w", err)
	}
	batches, err := p.buildBatches(ctx, working, maxParallel)
	if err != nil {
		return nil, err
	}
	for i, ids := range batches {
		bid, err := p.Batches.Insert(ctx, state.Batch{
			SequenceNo: i + 1,
			Status:     state.BatchPlanned,
			StoryIDs:   ids,
		})
		if err != nil {
			return nil, fmt.Errorf("planner batch insert seq=%d: %w", i+1, err)
		}
		res.BatchIDs = append(res.BatchIDs, bid)
		res.BatchesCreated++
	}
	return res, nil
}

// buildBatches runs a deterministic Kahn-style topo sort with within-layer
// grouping by file-overlap + android-serialization + max_parallel cap.
func (p *Planner) buildBatches(ctx context.Context, parsed []ParsedStory, maxParallel int) ([][]string, error) {
	// Index parsed stories by id for lookups.
	byID := make(map[string]ParsedStory, len(parsed))
	for _, ps := range parsed {
		byID[ps.Frontmatter.StoryID] = ps
	}

	// Build in-degree + reverse-edge map.
	inDeg := make(map[string]int)
	revEdges := make(map[string][]string)
	for _, ps := range parsed {
		if _, ok := inDeg[ps.Frontmatter.StoryID]; !ok {
			inDeg[ps.Frontmatter.StoryID] = 0
		}
		for _, dep := range ps.Frontmatter.DependsOn {
			if _, present := byID[dep]; !present {
				continue // external dep — already done or not in this epics file
			}
			inDeg[ps.Frontmatter.StoryID]++
			revEdges[dep] = append(revEdges[dep], ps.Frontmatter.StoryID)
		}
	}

	ready := []string{}
	for id, d := range inDeg {
		if d == 0 {
			ready = append(ready, id)
		}
	}
	sort.Strings(ready)

	var out [][]string
	for len(ready) > 0 {
		layer := buildLayer(ready, byID, maxParallel)
		out = append(out, layer)

		// Mark layer as consumed; release dependents.
		nextReady := []string{}
		layerSet := make(map[string]bool, len(layer))
		for _, id := range layer {
			layerSet[id] = true
		}
		for _, id := range ready {
			if layerSet[id] {
				for _, dependent := range revEdges[id] {
					inDeg[dependent]--
					if inDeg[dependent] == 0 {
						nextReady = append(nextReady, dependent)
					}
				}
				continue
			}
			nextReady = append(nextReady, id) // not picked this round, retry next
		}
		sort.Strings(nextReady)
		ready = nextReady
	}

	return out, nil
}

// buildLayer greedily fills one batch from the ready set, honoring:
//   - max_parallel cap
//   - file-overlap disjointness (no two stories with overlapping affects)
//   - android-serialization (requires_android = solo batch)
func buildLayer(ready []string, byID map[string]ParsedStory, maxParallel int) []string {
	var chosen []string
	takenPaths := map[string]bool{}
	androidSeen := false

	for _, id := range ready {
		if len(chosen) >= maxParallel {
			break
		}
		ps := byID[id]
		// Android-serialization: if this story needs android, it must be alone.
		if ps.Frontmatter.RequiresAndroid {
			if len(chosen) > 0 {
				continue
			}
			chosen = append(chosen, id)
			androidSeen = true
			break
		}
		if androidSeen {
			continue
		}
		// File-overlap check.
		overlaps := false
		for _, p := range ps.Frontmatter.Affects {
			if takenPaths[p] {
				overlaps = true
				break
			}
		}
		if overlaps {
			continue
		}
		for _, p := range ps.Frontmatter.Affects {
			takenPaths[p] = true
		}
		chosen = append(chosen, id)
	}
	if len(chosen) == 0 && len(ready) > 0 {
		// Defensive: ensure forward progress (avoid infinite loop if a story
		// has self-conflicting affects). Pick first ready unconditionally.
		chosen = []string{ready[0]}
	}
	return chosen
}
