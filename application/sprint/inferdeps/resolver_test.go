package inferdeps_test

import (
	"testing"

	"github.com/sosalejandro/bmad-story-runner-cli/application/sprint/inferdeps"
)

func TestResolve_ExplicitStoryReferenceIsHIGH(t *testing.T) {
	t.Parallel()
	path := writeEpics(t, `## Epic 4: Slice 1 — identity BC

### Story 4.1: Identity Aggregates

- **Given** baseline
- **Refs:** FR-1

### Story 4.2: Identity Canonical Service

- **Given** Story 4.1 emits events through outbox
- **Refs:** FR-2
`)
	parsed, _ := inferdeps.ParseEpics(path)
	sugs := inferdeps.Resolve(parsed, false)
	if len(sugs) != 2 {
		t.Fatalf("len=%d, want 2", len(sugs))
	}
	s2 := sugs[1]
	if len(s2.InferredDeps) != 1 || s2.InferredDeps[0].DepID != "4.1" {
		t.Fatalf("inferred = %+v", s2.InferredDeps)
	}
	if s2.InferredDeps[0].Confidence != inferdeps.ConfidenceHigh {
		t.Errorf("conf = %v, want HIGH", s2.InferredDeps[0].Confidence)
	}
}

func TestResolve_SliceMentionResolvesToLastStoryOfEpicMEDIUM(t *testing.T) {
	t.Parallel()
	path := writeEpics(t, `## Epic 1: Slice 0a — Reference

### Story 1.1: Pick BC
### Story 1.2: Doc Helper
### Story 1.3: Walkthrough
### Story 1.4: task check green

## Epic 4: Slice 1 — identity

### Story 4.1: Identity Aggregates

- **Given** Slice 0a complete; saveWithEvents helper available
- **Refs:** FR-Arch-1
`)
	parsed, _ := inferdeps.ParseEpics(path)
	sugs := inferdeps.Resolve(parsed, false)
	// Story 4.1 should infer dep on 1.4 (last story of epic 1, which owns slice 0a).
	var s41 inferdeps.Suggestion
	for _, s := range sugs {
		if s.StoryID == "4.1" {
			s41 = s
		}
	}
	if len(s41.InferredDeps) != 1 || s41.InferredDeps[0].DepID != "1.4" {
		t.Fatalf("4.1 inferred = %+v", s41.InferredDeps)
	}
	if s41.InferredDeps[0].Confidence != inferdeps.ConfidenceMedium {
		t.Errorf("conf = %v, want MEDIUM", s41.InferredDeps[0].Confidence)
	}
}

func TestResolve_IntraEpicFallbackLOWOnlyWhenEnabled(t *testing.T) {
	t.Parallel()
	path := writeEpics(t, `## Epic 1: Slice 0a

### Story 1.1: First
### Story 1.2: Second

- **Given** the previous story completed
`)
	parsed, _ := inferdeps.ParseEpics(path)

	noFallback := inferdeps.Resolve(parsed, false)
	for _, s := range noFallback {
		if s.StoryID == "1.2" && len(s.InferredDeps) != 0 {
			t.Errorf("with fallback OFF, 1.2 inferred = %+v", s.InferredDeps)
		}
	}

	withFallback := inferdeps.Resolve(parsed, true)
	var s12 inferdeps.Suggestion
	for _, s := range withFallback {
		if s.StoryID == "1.2" {
			s12 = s
		}
	}
	if len(s12.InferredDeps) != 1 || s12.InferredDeps[0].DepID != "1.1" {
		t.Fatalf("with fallback ON, 1.2 inferred = %+v", s12.InferredDeps)
	}
	if s12.InferredDeps[0].Confidence != inferdeps.ConfidenceLow {
		t.Errorf("conf = %v, want LOW", s12.InferredDeps[0].Confidence)
	}
}

func TestResolve_NoSelfLoopFromSameEpicSliceMention(t *testing.T) {
	t.Parallel()
	path := writeEpics(t, `## Epic 1: Slice 0a

### Story 1.2: Second

- **Given** Slice 0a foundation in place
`)
	parsed, _ := inferdeps.ParseEpics(path)
	sugs := inferdeps.Resolve(parsed, false)
	if len(sugs[0].InferredDeps) != 0 {
		t.Errorf("self-epic slice mention should not infer dep; got %+v", sugs[0].InferredDeps)
	}
}

func TestResolve_DedupesAndOrdersByConfidence(t *testing.T) {
	t.Parallel()
	path := writeEpics(t, `## Epic 1: Slice 0a

### Story 1.1: A
### Story 1.2: B

## Epic 4: Slice 1

### Story 4.1: Mix

- **Given** Story 1.2 ready and Slice 0a foundation done
`)
	parsed, _ := inferdeps.ParseEpics(path)
	sugs := inferdeps.Resolve(parsed, false)
	var s41 inferdeps.Suggestion
	for _, s := range sugs {
		if s.StoryID == "4.1" {
			s41 = s
		}
	}
	// Should dedupe: "Slice 0a" → 1.2 (last of epic 1), "Story 1.2" → 1.2 explicit.
	// Final list: one entry, HIGH confidence (HIGH wins via stable sort).
	if len(s41.InferredDeps) != 1 {
		t.Fatalf("expected 1 deduped dep, got %+v", s41.InferredDeps)
	}
	if s41.InferredDeps[0].DepID != "1.2" {
		t.Errorf("dep id = %s, want 1.2", s41.InferredDeps[0].DepID)
	}
	if s41.InferredDeps[0].Confidence != inferdeps.ConfidenceHigh {
		t.Errorf("conf = %v, want HIGH (HIGH must precede MEDIUM in dedup)", s41.InferredDeps[0].Confidence)
	}
}

func TestResolve_MergedAndNewAndMatchedDeps(t *testing.T) {
	t.Parallel()
	path := writeEpics(t, `## Epic 4: Slice 1

### Story 4.2: Canonical Service

---
story_id: "4.2"
depends_on: ["4.1"]
---

- **Given** Story 4.1 emits events
- **Given** Story 3.6 toolchain bumped
`)
	parsed, _ := inferdeps.ParseEpics(path)
	// 3.6 doesn't exist in source → filtered out. 4.1 is matched.
	sugs := inferdeps.Resolve(parsed, false)
	s := sugs[0]
	// 4.1 doesn't exist either in this minimal fixture → it'd be filtered.
	// Re-do with a richer fixture that contains 4.1.
	path = writeEpics(t, `## Epic 3: Slice 0c

### Story 3.6: Bump

## Epic 4: Slice 1

### Story 4.1: Aggregates

### Story 4.2: Canonical Service

---
story_id: "4.2"
depends_on: ["4.1"]
---

- **Given** Story 4.1 emits events
- **Given** Story 3.6 toolchain bumped
`)
	parsed, _ = inferdeps.ParseEpics(path)
	sugs = inferdeps.Resolve(parsed, false)
	for _, s = range sugs {
		if s.StoryID == "4.2" {
			break
		}
	}
	merged := s.MergedDeps()
	if len(merged) != 2 || merged[0] != "3.6" || merged[1] != "4.1" {
		t.Errorf("merged = %v, want [3.6 4.1]", merged)
	}
	newDeps := s.NewDeps()
	if len(newDeps) != 1 || newDeps[0].DepID != "3.6" {
		t.Errorf("new = %+v, want [3.6]", newDeps)
	}
	matched := s.MatchedDeps()
	if len(matched) != 1 || matched[0].DepID != "4.1" {
		t.Errorf("matched = %+v, want [4.1]", matched)
	}
}

func TestResolve_NaturalOrderingOfMergedDeps(t *testing.T) {
	t.Parallel()
	path := writeEpics(t, `## Epic 4: Slice 1

### Story 4.1: A
### Story 4.2: B
### Story 4.10: J

### Story 4.11: K

- **Given** Story 4.2 and Story 4.10 and Story 4.1 ready
`)
	parsed, _ := inferdeps.ParseEpics(path)
	sugs := inferdeps.Resolve(parsed, false)
	var s inferdeps.Suggestion
	for _, x := range sugs {
		if x.StoryID == "4.11" {
			s = x
		}
	}
	merged := s.MergedDeps()
	if len(merged) != 3 || merged[0] != "4.1" || merged[1] != "4.2" || merged[2] != "4.10" {
		t.Errorf("merged = %v, want [4.1 4.2 4.10]", merged)
	}
}
