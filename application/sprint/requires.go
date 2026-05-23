package sprint

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// SynthesisResult captures the planner-side outcome of expanding
// epic-level `requires_epics:` / `requires_stories:` into per-story
// dependency edges (issue #46).
type SynthesisResult struct {
	// SynthesizedDeps maps storyID → the cross-epic edges synthesized
	// for it. These are ADDITIVE to whatever the story's own
	// `depends_on:` declared. Stories whose epic has no
	// `requires_epics:` / `requires_stories:` (and that didn't opt out
	// via story-level `bypass_epic_requires:`) get an empty slice.
	//
	// The slice is de-duplicated against the story's own DependsOn, so
	// `len(SynthesizedDeps[id])` is the count of NEW edges only — not
	// the total deps of that story.
	SynthesizedDeps map[string][]string

	// SynthesizedKinds parallels SynthesizedDeps and records WHICH kind
	// of synthesis produced each edge — 'epic_synth' (from
	// requires_epics:) vs 'epic_synth_stories' (from requires_stories:).
	// Same index ordering as SynthesizedDeps[id], so callers can correlate
	// dep id ↔ kind without a second map lookup. Issue #54.
	SynthesizedKinds map[string][]SynthEdgeKind

	// Warnings collects non-fatal diagnostics the planner should
	// surface to the operator (placeholder linear-chain smell, missing
	// referenced epics, etc.). Empty when the input was clean.
	Warnings []string
}

// SynthEdgeKind discriminates the synthesis source. Distinct from
// domain/state.DependencyEdgeKind so the application layer doesn't pull
// the persistence-shaped enum into pure-function space; the planner maps
// between the two when it persists.
type SynthEdgeKind string

const (
	// SynthFromEpicRequires — generated from `requires_epics: [N]`.
	SynthFromEpicRequires SynthEdgeKind = "epic_synth"
	// SynthFromStoryRequires — generated from `requires_stories: [...]`.
	SynthFromStoryRequires SynthEdgeKind = "epic_synth_stories"
)

// SynthesizeRequiresEpics walks every epic + its stories and computes the
// `requires_epics:` / `requires_stories:` expansion. Pure function — no
// IO, no logging, no side effects. The planner persists the result.
//
// Semantics (per issue #46):
//
//  1. For each epic E with non-empty RequiresEpics:
//     every story S in E implicitly depends_on the last story of each
//     epic R in E.RequiresEpics (R != E.EpicID). "Last story" = the
//     story in epic R with the highest natural-ordinal id in document
//     order (e.g. "4.10" > "4.2", not lex-sort).
//
//  2. For each epic E with non-empty RequiresStories:
//     every story S in E implicitly depends_on each listed story id —
//     verbatim, regardless of which epic owns it.
//
//  3. Story-level overrides are ADDITIVE only. The single way to suppress
//     an epic-level requirement for a specific story is to list the
//     unwanted epic id in that story's `bypass_epic_requires:` slice.
//
//  4. Self-reference (epic E requiring its own id) is silently dropped.
//
//  5. References to non-existent epics / stories produce a warning but
//     do NOT fail synthesis — the planner already tolerates dangling
//     `depends_on:` entries (treated as "external prerequisite, done
//     elsewhere"). Surface them so an operator can decide whether
//     they're typos.
func SynthesizeRequiresEpics(epics []ParsedEpic, stories []ParsedStory) SynthesisResult {
	res := SynthesisResult{
		SynthesizedDeps:  make(map[string][]string, len(stories)),
		SynthesizedKinds: make(map[string][]SynthEdgeKind, len(stories)),
	}

	// Index: epic id → its parsed header (so we can look up RequiresEpics
	// at synthesis time). Built first so warnings for unknown referenced
	// epics fire even when no story in this file lives in that epic.
	epicByID := make(map[string]ParsedEpic, len(epics))
	for _, e := range epics {
		epicByID[e.EpicID] = e
	}

	// Index: epic id → its stories, in document order. The planner needs
	// "last story of epic R" — defined as the story with the highest
	// natural-ordinal id within R (so "4.10" wins over "4.2", not the
	// other way around as plain string-sort would say).
	storiesByEpic := make(map[string][]ParsedStory, len(epics))
	allStoryIDs := make(map[string]struct{}, len(stories))
	for _, s := range stories {
		ep := s.EpicID
		if ep == "" {
			ep = EpicIDFromStory(s.Frontmatter.StoryID)
		}
		storiesByEpic[ep] = append(storiesByEpic[ep], s)
		allStoryIDs[s.Frontmatter.StoryID] = struct{}{}
	}

	// Last-story lookup. Sort each epic's story slice by natural ordinal
	// once and grab the tail. Documenting the ordering is critical because
	// "highest ordinal" is the user-visible contract.
	lastStoryOfEpic := make(map[string]string, len(storiesByEpic))
	for ep, ss := range storiesByEpic {
		sorted := make([]ParsedStory, len(ss))
		copy(sorted, ss)
		sort.Slice(sorted, func(i, j int) bool {
			return naturalLess(sorted[i].Frontmatter.StoryID, sorted[j].Frontmatter.StoryID)
		})
		lastStoryOfEpic[ep] = sorted[len(sorted)-1].Frontmatter.StoryID
	}

	// Synthesise per story.
	for _, s := range stories {
		ep := s.EpicID
		if ep == "" {
			ep = EpicIDFromStory(s.Frontmatter.StoryID)
		}
		owner, ok := epicByID[ep]
		if !ok {
			// Story is orphaned — no parent epic header captured. Common
			// when a freshly-drafted epics.md skips the `## Epic N` line.
			// Skip synthesis (story-level deps still apply).
			continue
		}
		// `bypass_epic_requires:` is story-level — turn into a set for O(1)
		// containment checks.
		bypass := make(map[int]struct{}, len(s.Frontmatter.BypassEpicRequires))
		for _, b := range s.Frontmatter.BypassEpicRequires {
			bypass[b] = struct{}{}
		}

		// Track synth edges we've already produced for this story so the
		// output slice doesn't contain duplicates (e.g. the same target id
		// would-be-emitted via both requires_epics and requires_stories).
		// We intentionally do NOT pre-seed this with the author's
		// depends_on — the planner needs the synth attribution for every
		// epic-level edge even when the author independently wrote the
		// same target, so the persisted edge_kind reflects the upstream
		// declaration (issue #54). The planner's combine-step (in
		// PlanWithEpics) is the single place that dedupes synth vs author
		// for the "additional edges added" count.
		seen := make(map[string]struct{}, len(s.Frontmatter.DependsOn)+4)
		seen[s.Frontmatter.StoryID] = struct{}{} // never self-depend

		// Track edges + their attribution. Parallel slices stay in lockstep
		// so the final sort can carry both columns together.
		var synth []string
		var kinds []SynthEdgeKind

		// (a) requires_epics expansion: last story of each referenced epic
		for _, ref := range owner.Frontmatter.RequiresEpics {
			refStr := strconv.Itoa(ref)
			if refStr == ep {
				continue // self-reference, silently dropped
			}
			if _, exists := bypass[ref]; exists {
				continue
			}
			last, found := lastStoryOfEpic[refStr]
			if !found {
				// Referenced epic has no stories in this file — record a
				// warning and skip. A real referenced epic with zero
				// stories is almost certainly a misnumbering.
				res.Warnings = append(res.Warnings, fmt.Sprintf(
					"epic %s requires_epics: [%d] — referenced epic has no stories in this epics.md",
					ep, ref))
				continue
			}
			if _, dup := seen[last]; dup {
				continue
			}
			seen[last] = struct{}{}
			synth = append(synth, last)
			kinds = append(kinds, SynthFromEpicRequires)
		}

		// (b) requires_stories expansion: literal cross-cutting pins
		for _, pin := range owner.Frontmatter.RequiresStories {
			if pin == "" {
				continue
			}
			if _, exists := allStoryIDs[pin]; !exists {
				res.Warnings = append(res.Warnings, fmt.Sprintf(
					"epic %s requires_stories: [%q] — referenced story not present in this epics.md",
					ep, pin))
				// Still add the edge — the planner tolerates external
				// prerequisites. This matches existing `depends_on:` handling.
			}
			if _, dup := seen[pin]; dup {
				continue
			}
			seen[pin] = struct{}{}
			synth = append(synth, pin)
			kinds = append(kinds, SynthFromStoryRequires)
		}

		if len(synth) > 0 {
			// Deterministic order: natural-id ascending. We co-sort kinds
			// using an index permutation so a row keeps its attribution
			// even when stories_requires-pinned ids interleave with
			// requires_epics-derived ones.
			idx := make([]int, len(synth))
			for i := range idx {
				idx[i] = i
			}
			sort.Slice(idx, func(i, j int) bool { return naturalLess(synth[idx[i]], synth[idx[j]]) })
			sortedDeps := make([]string, len(synth))
			sortedKinds := make([]SynthEdgeKind, len(synth))
			for i, src := range idx {
				sortedDeps[i] = synth[src]
				sortedKinds[i] = kinds[src]
			}
			res.SynthesizedDeps[s.Frontmatter.StoryID] = sortedDeps
			res.SynthesizedKinds[s.Frontmatter.StoryID] = sortedKinds
		}
	}

	return res
}

// DetectRequiresEpicsCycle walks the epic-level requires_epics graph
// looking for cycles (A.requires_epics: [B] + B.requires_epics: [A] —
// acceptance criterion #4 of issue #46). Returns the cycle as a slice of
// epic ids in traversal order when found, or an empty slice when the
// graph is acyclic. Pure function.
//
// We only check the epic-level graph because story-level cycles (a story
// in epic A explicitly depending on a story in epic B that already
// requires epic A) are detected later by the topo-sort layer in
// buildBatches — the planner already exits with "no progress" when the
// in-degree map can't drain. The epic-level check is the cheap up-front
// guard.
func DetectRequiresEpicsCycle(epics []ParsedEpic) []string {
	// Build adjacency: epic id → epics it requires (as strings, stripping
	// self-loops because those are silently dropped at synthesis time).
	adj := make(map[string][]string, len(epics))
	known := make(map[string]struct{}, len(epics))
	for _, e := range epics {
		known[e.EpicID] = struct{}{}
	}
	for _, e := range epics {
		for _, r := range e.Frontmatter.RequiresEpics {
			refStr := strconv.Itoa(r)
			if refStr == e.EpicID {
				continue
			}
			if _, ok := known[refStr]; !ok {
				// Referenced epic isn't in the file — can't form a cycle
				// without both endpoints present. Synthesis-time warning
				// surfaces this separately.
				continue
			}
			adj[e.EpicID] = append(adj[e.EpicID], refStr)
		}
	}

	const (
		white = 0 // unvisited
		gray  = 1 // in current DFS stack
		black = 2 // fully explored
	)
	color := make(map[string]int, len(epics))
	var (
		stack []string
		cycle []string
	)

	var dfs func(n string) bool
	dfs = func(n string) bool {
		color[n] = gray
		stack = append(stack, n)
		for _, m := range adj[n] {
			switch color[m] {
			case white:
				if dfs(m) {
					return true
				}
			case gray:
				// Found back-edge — extract the cycle from the DFS stack.
				for i, v := range stack {
					if v == m {
						cycle = append([]string(nil), stack[i:]...)
						cycle = append(cycle, m) // close the loop visually
						return true
					}
				}
				cycle = []string{m, n, m}
				return true
			}
		}
		stack = stack[:len(stack)-1]
		color[n] = black
		return false
	}

	// Iterate epics in document order so the surfaced cycle is the
	// first one a left-to-right reader would hit.
	for _, e := range epics {
		if color[e.EpicID] == white {
			stack = stack[:0]
			if dfs(e.EpicID) {
				return cycle
			}
		}
	}
	return nil
}

// DetectLinearChainPlaceholderSmell scans for the "first story of Epic N
// depends_on last story of Epic N-1, with NO requires_epics: declared"
// pattern (acceptance criterion #5 of issue #46). It's the canonical
// shape of a conservative placeholder dependency authors add when they're
// unsure if there's a real cross-epic ordering — and the one the EDA
// cutover wasted two days untangling. Surfacing it as a warning lets the
// operator decide if it's a real prereq or a placeholder ready to drop.
//
// Returns one human-readable warning string per smelled story (empty
// slice when nothing matches). Pure function.
func DetectLinearChainPlaceholderSmell(epics []ParsedEpic, stories []ParsedStory) []string {
	if len(epics) < 2 {
		return nil
	}

	// Index epics by id + by document order. We need both: (a) is this
	// epic the immediate successor of the one its first story depends on?
	// (b) does this epic declare `requires_epics:` — if so, suppress the
	// warning since the author has explicitly opted in.
	epicByID := make(map[string]ParsedEpic, len(epics))
	for _, e := range epics {
		epicByID[e.EpicID] = e
	}

	// Index stories per epic in natural-id order, so we can identify
	// "first" deterministically.
	storiesByEpic := make(map[string][]ParsedStory, len(epics))
	for _, s := range stories {
		ep := s.EpicID
		if ep == "" {
			ep = EpicIDFromStory(s.Frontmatter.StoryID)
		}
		storiesByEpic[ep] = append(storiesByEpic[ep], s)
	}
	for ep, ss := range storiesByEpic {
		sort.Slice(ss, func(i, j int) bool {
			return naturalLess(ss[i].Frontmatter.StoryID, ss[j].Frontmatter.StoryID)
		})
		storiesByEpic[ep] = ss
	}

	lastStoryOfEpic := make(map[string]string, len(storiesByEpic))
	for ep, ss := range storiesByEpic {
		lastStoryOfEpic[ep] = ss[len(ss)-1].Frontmatter.StoryID
	}

	var warnings []string
	for i := 1; i < len(epics); i++ {
		cur := epics[i]
		prev := epics[i-1]
		// Skip when the current epic has already opted in to epic-level
		// dependencies — author is being explicit, no smell.
		if len(cur.Frontmatter.RequiresEpics) > 0 || len(cur.Frontmatter.RequiresStories) > 0 {
			continue
		}
		// Need a previous epic with at least one story (to be the
		// "last story" target) and a current epic with a first story.
		prevLast, hasPrev := lastStoryOfEpic[prev.EpicID]
		if !hasPrev {
			continue
		}
		curStories := storiesByEpic[cur.EpicID]
		if len(curStories) == 0 {
			continue
		}
		first := curStories[0]
		// First story explicitly depends on the previous epic's last
		// story → looks like a placeholder.
		for _, dep := range first.Frontmatter.DependsOn {
			if dep == prevLast {
				warnings = append(warnings, fmt.Sprintf(
					"epic %s: story %s depends_on %s (last story of epic %s) but epic %s declares no requires_epics: — likely a placeholder linear chain (issue #46)",
					cur.EpicID, first.Frontmatter.StoryID, prevLast, prev.EpicID, cur.EpicID))
				break
			}
		}
	}
	return warnings
}

// naturalLess compares two story-id strings as humans expect:
//
//	"4.2"  <  "4.10"      (numeric within each dot segment)
//	"4.1.payment" < "4.2" (segment-wise, falls back to string compare on
//	                       non-numeric segments)
//
// Mirrors inferdeps.naturalLess but kept local to keep package
// dependencies acyclic.
func naturalLess(a, b string) bool {
	pa := strings.Split(a, ".")
	pb := strings.Split(b, ".")
	for i := 0; i < len(pa) && i < len(pb); i++ {
		ad := isAllDigits(pa[i])
		bd := isAllDigits(pb[i])
		if ad && bd {
			ai, _ := strconv.Atoi(pa[i])
			bi, _ := strconv.Atoi(pb[i])
			if ai != bi {
				return ai < bi
			}
			continue
		}
		if pa[i] != pb[i] {
			return pa[i] < pb[i]
		}
	}
	return len(pa) < len(pb)
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
