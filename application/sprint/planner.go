package sprint

import (
	"context"
	"fmt"
	"sort"

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
	StoriesInserted    int     `json:"stories_inserted"`
	StoriesUpdated     int     `json:"stories_updated"`
	DependenciesAdded  int     `json:"dependencies_added"`
	AffectsAdded       int     `json:"affects_added"`
	BatchesCreated     int     `json:"batches_created"`
	BatchIDs           []int64 `json:"batch_ids"`
	// Scope, when non-empty, is the epic-id filter that was applied. Stories
	// outside this scope were skipped entirely.
	Scope              string  `json:"scope,omitempty"`
	// StoriesSkippedByScope counts how many parsed stories were excluded
	// because they don't belong to the requested --scope.
	StoriesSkippedByScope int `json:"stories_skipped_by_scope,omitempty"`
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
// batches first (re-planning replaces the queue). Idempotent on stories +
// dependencies + affects (INSERT OR IGNORE under the hood).
func (p *Planner) Plan(ctx context.Context, parsed []ParsedStory, maxParallel int) (*IngestResult, error) {
	if maxParallel <= 0 {
		maxParallel = 4
	}
	res := &IngestResult{}

	for _, ps := range parsed {
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
	batches, err := p.buildBatches(ctx, parsed, maxParallel)
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
