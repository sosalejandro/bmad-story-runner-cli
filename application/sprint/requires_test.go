package sprint_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sosalejandro/bmad-story-runner-cli/application/sprint"
	"github.com/sosalejandro/bmad-story-runner-cli/domain/state"
	"github.com/sosalejandro/bmad-story-runner-cli/infrastructure/state/sqlite"
)

// fourEpicSyntheticFixture is the canonical issue #46 acceptance fixture:
// four epics encoding every topology the resolver has to handle.
//
//	Epic 1: foundation (no requires)         — 1.1, 1.2, 1.3
//	Epic 2: linear chain to 1                — 2.1, 2.2     requires_epics: [1]
//	Epic 3: fan-out from 1                   — 3.1, 3.2     requires_epics: [1]
//	Epic 4: diamond + multi-deps             — 4.1, 4.2     requires_epics: [2, 3]
//	                                                       requires_stories: ["1.2"]
//
// After synthesis the planner-expected edges are:
//
//	2.1 → 1.3        (last of epic 1)
//	2.2 → 1.3        (inherited from epic 2's requires_epics)
//	3.1 → 1.3        (fan-out from epic 1)
//	3.2 → 1.3
//	4.1 → 2.2, 3.2, 1.2  (diamond + the cross-cutting story pin)
//	4.2 → 2.2, 3.2, 1.2
const fourEpicSyntheticFixture = `# Synthetic 4-Epic DAG (issue #46 acceptance)

## Epic 1: Foundation

### Story 1.1: Bootstrap

---
story_id: "1.1"
---

### Story 1.2: Cross-cutting Helper

---
story_id: "1.2"
---

### Story 1.3: Last of Epic 1

---
story_id: "1.3"
---

## Epic 2: Linear Chain to Epic 1

---
epic_id: 2
requires_epics: [1]
---

### Story 2.1: First of Epic 2

---
story_id: "2.1"
---

### Story 2.2: Last of Epic 2

---
story_id: "2.2"
---

## Epic 3: Fan-out from Epic 1

---
epic_id: 3
requires_epics: [1]
---

### Story 3.1: First of Epic 3

---
story_id: "3.1"
---

### Story 3.2: Last of Epic 3

---
story_id: "3.2"
---

## Epic 4: Diamond + Multi-deps

---
epic_id: 4
requires_epics: [2, 3]
requires_stories: ["1.2"]
---

### Story 4.1: First of Epic 4

---
story_id: "4.1"
---

### Story 4.2: Last of Epic 4

---
story_id: "4.2"
---
`

func writeFixture(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

func TestParseEpicsFileFull_CapturesEpicRequiresFrontmatter(t *testing.T) {
	t.Parallel()
	path := writeFixture(t, "epics.md", fourEpicSyntheticFixture)

	parsed, err := sprint.ParseEpicsFileFull(path)
	if err != nil {
		t.Fatalf("ParseEpicsFileFull: %v", err)
	}
	if len(parsed.Epics) != 4 {
		t.Fatalf("epics = %d, want 4", len(parsed.Epics))
	}
	if len(parsed.Stories) != 9 {
		t.Fatalf("stories = %d, want 9 (3+2+2+2)", len(parsed.Stories))
	}

	// Epic 1: no requires
	if parsed.Epics[0].EpicID != "1" {
		t.Errorf("epics[0].EpicID = %q, want 1", parsed.Epics[0].EpicID)
	}
	if len(parsed.Epics[0].Frontmatter.RequiresEpics) != 0 {
		t.Errorf("epic 1 should have no requires_epics; got %v", parsed.Epics[0].Frontmatter.RequiresEpics)
	}

	// Epic 4: requires [2,3] + requires_stories: ["1.2"]
	got := parsed.Epics[3]
	if got.EpicID != "4" {
		t.Errorf("epics[3].EpicID = %q, want 4", got.EpicID)
	}
	if got.Frontmatter.EpicID != 4 {
		t.Errorf("epics[3].Frontmatter.EpicID = %d, want 4", got.Frontmatter.EpicID)
	}
	if len(got.Frontmatter.RequiresEpics) != 2 || got.Frontmatter.RequiresEpics[0] != 2 || got.Frontmatter.RequiresEpics[1] != 3 {
		t.Errorf("epic 4 requires_epics = %v, want [2,3]", got.Frontmatter.RequiresEpics)
	}
	if len(got.Frontmatter.RequiresStories) != 1 || got.Frontmatter.RequiresStories[0] != "1.2" {
		t.Errorf("epic 4 requires_stories = %v, want [1.2]", got.Frontmatter.RequiresStories)
	}
	if !got.HasFrontmatter {
		t.Errorf("epic 4: HasFrontmatter = false, want true")
	}

	// Every story carries its parent epic id (so the resolver can group them).
	wantEpics := map[string]string{
		"1.1": "1", "1.2": "1", "1.3": "1",
		"2.1": "2", "2.2": "2",
		"3.1": "3", "3.2": "3",
		"4.1": "4", "4.2": "4",
	}
	for _, s := range parsed.Stories {
		if got, want := s.EpicID, wantEpics[s.Frontmatter.StoryID]; got != want {
			t.Errorf("story %s EpicID = %q, want %q", s.Frontmatter.StoryID, got, want)
		}
	}
}

func TestSynthesizeRequiresEpics_FourEpicDAG(t *testing.T) {
	t.Parallel()
	path := writeFixture(t, "epics.md", fourEpicSyntheticFixture)
	parsed, err := sprint.ParseEpicsFileFull(path)
	if err != nil {
		t.Fatalf("ParseEpicsFileFull: %v", err)
	}
	got := sprint.SynthesizeRequiresEpics(parsed.Epics, parsed.Stories)

	want := map[string][]string{
		// Epic 2 (linear chain to 1): every story gets last-of-1 = 1.3
		"2.1": {"1.3"},
		"2.2": {"1.3"},
		// Epic 3 (fan-out from 1): same
		"3.1": {"1.3"},
		"3.2": {"1.3"},
		// Epic 4 (diamond + 1.2 pin): last-of-2=2.2 + last-of-3=3.2 + 1.2
		"4.1": {"1.2", "2.2", "3.2"},
		"4.2": {"1.2", "2.2", "3.2"},
	}
	if len(got.SynthesizedDeps) != len(want) {
		t.Errorf("synthesised story count = %d, want %d (got %v)",
			len(got.SynthesizedDeps), len(want), got.SynthesizedDeps)
	}
	for sid, wantDeps := range want {
		gotDeps := got.SynthesizedDeps[sid]
		if !equalStringSlices(gotDeps, wantDeps) {
			t.Errorf("synth for %s = %v, want %v", sid, gotDeps, wantDeps)
		}
	}
	if len(got.Warnings) != 0 {
		t.Errorf("expected no warnings on clean fixture, got %v", got.Warnings)
	}
}

func TestSynthesizeRequiresEpics_BypassEpicRequires(t *testing.T) {
	t.Parallel()
	// 3.1 explicitly opts out of inheriting requires_epics: [1].
	path := writeFixture(t, "epics.md", `
## Epic 1
### Story 1.1: Foo
---
story_id: "1.1"
---

## Epic 3

---
epic_id: 3
requires_epics: [1]
---

### Story 3.1: opts out

---
story_id: "3.1"
bypass_epic_requires: [1]
---

### Story 3.2: still inherits

---
story_id: "3.2"
---
`)
	parsed, err := sprint.ParseEpicsFileFull(path)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	res := sprint.SynthesizeRequiresEpics(parsed.Epics, parsed.Stories)

	if _, present := res.SynthesizedDeps["3.1"]; present {
		t.Errorf("3.1 bypassed epic 1 but still got synthesized deps: %v", res.SynthesizedDeps["3.1"])
	}
	if got := res.SynthesizedDeps["3.2"]; !equalStringSlices(got, []string{"1.1"}) {
		t.Errorf("3.2 inherited deps = %v, want [1.1]", got)
	}
}

func TestSynthesizeRequiresEpics_SelfReferenceIsDropped(t *testing.T) {
	t.Parallel()
	// Epic 1 listing itself in requires_epics → no synthesized edges for any
	// 1.* story (would create a self-cycle otherwise).
	path := writeFixture(t, "epics.md", `## Epic 1

---
epic_id: 1
requires_epics: [1]
---

### Story 1.1: Foo
---
story_id: "1.1"
---

### Story 1.2
---
story_id: "1.2"
---
`)
	parsed, err := sprint.ParseEpicsFileFull(path)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	res := sprint.SynthesizeRequiresEpics(parsed.Epics, parsed.Stories)
	if len(res.SynthesizedDeps) != 0 {
		t.Errorf("self-reference produced synthesized deps: %v", res.SynthesizedDeps)
	}
}

func TestSynthesizeRequiresEpics_UnknownEpicWarns(t *testing.T) {
	t.Parallel()
	path := writeFixture(t, "epics.md", `## Epic 4

---
epic_id: 4
requires_epics: [99]
---

### Story 4.1
---
story_id: "4.1"
---
`)
	parsed, _ := sprint.ParseEpicsFileFull(path)
	res := sprint.SynthesizeRequiresEpics(parsed.Epics, parsed.Stories)
	if len(res.Warnings) == 0 {
		t.Errorf("expected warning for unknown referenced epic 99, got none")
	}
	if len(res.SynthesizedDeps) != 0 {
		t.Errorf("synthesised deps for unknown epic = %v, want none", res.SynthesizedDeps)
	}
}

func TestDetectRequiresEpicsCycle_CatchesAB_BA(t *testing.T) {
	t.Parallel()
	path := writeFixture(t, "epics.md", `## Epic 1

---
epic_id: 1
requires_epics: [2]
---

### Story 1.1: Foo
---
story_id: "1.1"
---

## Epic 2

---
epic_id: 2
requires_epics: [1]
---

### Story 2.1
---
story_id: "2.1"
---
`)
	parsed, _ := sprint.ParseEpicsFileFull(path)
	cycle := sprint.DetectRequiresEpicsCycle(parsed.Epics)
	if len(cycle) == 0 {
		t.Fatalf("expected cycle detection, got nil")
	}
	// Cycle must visit both 1 and 2.
	joined := strings.Join(cycle, " ")
	if !strings.Contains(joined, "1") || !strings.Contains(joined, "2") {
		t.Errorf("cycle = %v should reference epics 1 and 2", cycle)
	}
}

func TestDetectRequiresEpicsCycle_AcyclicReturnsNil(t *testing.T) {
	t.Parallel()
	path := writeFixture(t, "epics.md", fourEpicSyntheticFixture)
	parsed, _ := sprint.ParseEpicsFileFull(path)
	if cycle := sprint.DetectRequiresEpicsCycle(parsed.Epics); cycle != nil {
		t.Errorf("acyclic fixture produced cycle: %v", cycle)
	}
}

func TestDetectLinearChainPlaceholderSmell_FlagsPlaceholderPattern(t *testing.T) {
	t.Parallel()
	// Story 2.1 depends on 1.last (= 1.2) but Epic 2 has NO requires_epics —
	// the canonical placeholder smell.
	path := writeFixture(t, "epics.md", `## Epic 1

### Story 1.1: Foo
---
story_id: "1.1"
---

### Story 1.2
---
story_id: "1.2"
---

## Epic 2

### Story 2.1
---
story_id: "2.1"
depends_on: ["1.2"]
---
`)
	parsed, _ := sprint.ParseEpicsFileFull(path)
	warnings := sprint.DetectLinearChainPlaceholderSmell(parsed.Epics, parsed.Stories)
	if len(warnings) == 0 {
		t.Fatalf("expected linear-chain smell warning, got none")
	}
	if !strings.Contains(warnings[0], "2.1") || !strings.Contains(warnings[0], "1.2") {
		t.Errorf("warning %q should mention the story+target", warnings[0])
	}
}

func TestDetectLinearChainPlaceholderSmell_SuppressedWhenRequiresEpicsDeclared(t *testing.T) {
	t.Parallel()
	// Same shape but Epic 2 declares requires_epics — author was explicit.
	// The smell-detector should stay quiet.
	path := writeFixture(t, "epics.md", `## Epic 1

### Story 1.1: Foo
---
story_id: "1.1"
---

## Epic 2

---
epic_id: 2
requires_epics: [1]
---

### Story 2.1
---
story_id: "2.1"
depends_on: ["1.1"]
---
`)
	parsed, _ := sprint.ParseEpicsFileFull(path)
	warnings := sprint.DetectLinearChainPlaceholderSmell(parsed.Epics, parsed.Stories)
	if len(warnings) != 0 {
		t.Errorf("expected no smell when requires_epics declared; got %v", warnings)
	}
}

// TestPlanWithEpics_FourEpicSyntheticDAG is the headline acceptance test
// (criterion #6 / #7 in issue #46): a 4-epic synthetic fixture exercises
// linear chain, fan-out, diamond, and multi-deps in one run, and the
// planner must:
//
//  1. Persist every synthesized cross-epic edge to story_dependencies
//  2. Produce a topo-sorted batch plan respecting both story-level AND
//     synthesised deps
//  3. Surface a non-zero SynthesizedDependenciesAdded count so an
//     operator can see how much came from epic-level frontmatter
func TestPlanWithEpics_FourEpicSyntheticDAG(t *testing.T) {
	t.Parallel()
	db := openDB(t)
	planner := &sprint.Planner{
		Stories:      sqlite.NewStoriesStore(db),
		Dependencies: sqlite.NewStoryDependenciesStore(db),
		Affects:      sqlite.NewStoryAffectsStore(db),
		Batches:      sqlite.NewBatchesStore(db),
	}

	path := writeFixture(t, "epics.md", fourEpicSyntheticFixture)
	parsed, err := sprint.ParseEpicsFileFull(path)
	if err != nil {
		t.Fatalf("ParseEpicsFileFull: %v", err)
	}
	ctx := context.Background()
	res, err := planner.PlanWithEpics(ctx, parsed.Epics, parsed.Stories, 10)
	if err != nil {
		t.Fatalf("PlanWithEpics: %v", err)
	}

	if res.SynthesizedDependenciesAdded == 0 {
		t.Errorf("SynthesizedDependenciesAdded = 0; want > 0 (resolver must have fired)")
	}
	if len(res.Warnings) != 0 {
		t.Errorf("clean fixture produced warnings: %v", res.Warnings)
	}

	// Persisted edges must match what SynthesizeRequiresEpics returned.
	depsStore := sqlite.NewStoryDependenciesStore(db)
	wantPersisted := map[string][]string{
		"2.1": {"1.3"},
		"2.2": {"1.3"},
		"3.1": {"1.3"},
		"3.2": {"1.3"},
		"4.1": {"1.2", "2.2", "3.2"},
		"4.2": {"1.2", "2.2", "3.2"},
	}
	for sid, want := range wantPersisted {
		got, err := depsStore.Of(ctx, sid)
		if err != nil {
			t.Fatalf("deps.Of %s: %v", sid, err)
		}
		if !equalStringSlices(got, want) {
			t.Errorf("persisted deps for %s = %v, want %v", sid, got, want)
		}
	}

	// Batch plan must produce four topological layers (Epic 1, then 2+3
	// parallel — but no batch can contain a story from Epic 4 alongside
	// its prerequisites). Lower bound is 3 layers; max 4 because the
	// planner may split 1.* across multiple batches if max_parallel is
	// hit — we set 10, so it shouldn't.
	if res.BatchesCreated < 3 {
		t.Errorf("BatchesCreated = %d; want >= 3 (Epic 1 layer, then dependents)", res.BatchesCreated)
	}
}

// TestPlanWithEpics_CycleErrors confirms acceptance criterion #4: the
// planner refuses to run when requires_epics forms a cycle, and does NOT
// touch SQLite (an aborted plan must not leave a half-applied state).
func TestPlanWithEpics_CycleErrors(t *testing.T) {
	t.Parallel()
	db := openDB(t)
	planner := &sprint.Planner{
		Stories:      sqlite.NewStoriesStore(db),
		Dependencies: sqlite.NewStoryDependenciesStore(db),
		Affects:      sqlite.NewStoryAffectsStore(db),
		Batches:      sqlite.NewBatchesStore(db),
	}

	path := writeFixture(t, "epics.md", `## Epic 1

---
epic_id: 1
requires_epics: [2]
---

### Story 1.1: Foo
---
story_id: "1.1"
---

## Epic 2

---
epic_id: 2
requires_epics: [1]
---

### Story 2.1
---
story_id: "2.1"
---
`)
	parsed, _ := sprint.ParseEpicsFileFull(path)
	if _, err := planner.PlanWithEpics(context.Background(), parsed.Epics, parsed.Stories, 4); err == nil {
		t.Fatalf("PlanWithEpics on cycle = nil; want error")
	} else if !strings.Contains(err.Error(), "cycle") {
		t.Errorf("error %q should mention cycle", err.Error())
	}

	// Defensive check: no stories were inserted before the cycle detector
	// fired (the cycle check is the first mutation gate).
	rows, err := sqlite.NewStoriesStore(db).List(context.Background(), state.StoryFilter{})
	if err != nil {
		t.Fatalf("list stories: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("planner inserted %d rows despite cycle; want 0 (atomicity)", len(rows))
	}
}

// TestPlanWithEpics_BackCompatNoFrontmatter confirms acceptance criterion
// "backwards compatibility": an epics.md WITHOUT any requires_epics:
// behaves exactly as the pre-#46 planner did. No synthesized edges, no
// warnings, no behavior shift on the resulting batch plan.
func TestPlanWithEpics_BackCompatNoFrontmatter(t *testing.T) {
	t.Parallel()
	db := openDB(t)
	planner := &sprint.Planner{
		Stories:      sqlite.NewStoriesStore(db),
		Dependencies: sqlite.NewStoryDependenciesStore(db),
		Affects:      sqlite.NewStoryAffectsStore(db),
		Batches:      sqlite.NewBatchesStore(db),
	}
	path := writeFixture(t, "epics.md", `## Epic 1

### Story 1.1: Foo
---
story_id: "1.1"
---

### Story 1.2
---
story_id: "1.2"
depends_on: ["1.1"]
---
`)
	parsed, _ := sprint.ParseEpicsFileFull(path)
	res, err := planner.PlanWithEpics(context.Background(), parsed.Epics, parsed.Stories, 10)
	if err != nil {
		t.Fatalf("PlanWithEpics: %v", err)
	}
	if res.SynthesizedDependenciesAdded != 0 {
		t.Errorf("no requires_epics declared but SynthesizedDependenciesAdded = %d", res.SynthesizedDependenciesAdded)
	}
	if res.DependenciesAdded != 1 {
		t.Errorf("DependenciesAdded = %d, want 1 (the explicit 1.2 → 1.1 edge)", res.DependenciesAdded)
	}
}

// equalStringSlices: order-sensitive equality, since SynthesizeRequiresEpics
// sorts its output deterministically (natural ordinal). Catches stable-sort
// regressions too.
func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
