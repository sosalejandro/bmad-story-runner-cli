package inferdeps_test

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/sosalejandro/bmad-story-runner-cli/application/sprint/inferdeps"
)

func TestEmitSummary_RendersNewDepsBlock(t *testing.T) {
	t.Parallel()
	sugs := []inferdeps.Suggestion{
		{
			StoryID: "4.2",
			Title:   "Identity Canonical Service",
			EpicID:  "4",
			FrontmatterDependsOn: []string{"4.1"},
			HasFrontmatter:       true,
			InferredDeps: []inferdeps.Inferred{
				{StoryID: "4.2", DepID: "4.1", Confidence: inferdeps.ConfidenceHigh, Cue: "Story 4.1 emits events", Source: "given"},
				{StoryID: "4.2", DepID: "1.4", Confidence: inferdeps.ConfidenceMedium, Cue: "Slice 0a complete", Source: "given"},
			},
		},
	}
	result := &inferdeps.PatchResult{
		EpicsFile: "/tmp/epics.md",
		Suggestions: sugs,
		TotalStories: 1,
		StoriesWithCues: 1,
		StoriesWithNew: 1,
	}
	var buf bytes.Buffer
	if err := inferdeps.EmitSummary(&buf, result); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "Story 4.2") {
		t.Errorf("missing story heading: %s", out)
	}
	if !strings.Contains(out, `depends_on: ["1.4", "4.1"]`) {
		t.Errorf("missing merged depends_on with 1.4 added: %s", out)
	}
	if !strings.Contains(out, "+ 1.4") {
		t.Errorf("missing new-dep annotation: %s", out)
	}
}

func TestComputeAgreement_OverlapBetweenInferredAndFrontmatter(t *testing.T) {
	t.Parallel()
	sugs := []inferdeps.Suggestion{
		{ // 1 match out of 1 inferred (100%)
			StoryID:              "4.1",
			FrontmatterDependsOn: []string{"3.6"},
			InferredDeps: []inferdeps.Inferred{
				{StoryID: "4.1", DepID: "3.6", Confidence: inferdeps.ConfidenceMedium},
			},
		},
		{ // 0 matches out of 1 inferred (0%) — tool found a new one
			StoryID:              "4.2",
			FrontmatterDependsOn: []string{"4.1"},
			InferredDeps: []inferdeps.Inferred{
				{StoryID: "4.2", DepID: "1.4", Confidence: inferdeps.ConfidenceMedium},
			},
		},
		{ // No inferred deps → not counted in score
			StoryID:              "4.3",
			FrontmatterDependsOn: []string{"4.2"},
		},
	}
	a := inferdeps.ComputeAgreement(sugs)
	if a.StoriesScored != 2 {
		t.Errorf("StoriesScored = %d, want 2", a.StoriesScored)
	}
	if a.InferredDepCount != 2 || a.MatchedDepCount != 1 {
		t.Errorf("counts = %d/%d, want 1/2", a.MatchedDepCount, a.InferredDepCount)
	}
	if a.AgreementRatePercent < 49 || a.AgreementRatePercent > 51 {
		t.Errorf("rate = %.1f, want ~50", a.AgreementRatePercent)
	}
}

func TestApplyPatches_RewritesInlineDependsOn(t *testing.T) {
	t.Parallel()
	src := `## Epic 4: Slice 1 — identity

### Story 4.2: Canonical Service

---
story_id: "4.2"
depends_on: ["4.1"]
complexity: medium
---

- **Given** Story 4.1 emits events
- **Given** Slice 0a helper available
`
	path := writeEpics(t, src)
	parsed, _ := inferdeps.ParseEpics(path)
	// craft a richer fixture with Epic 1 + 4.1 so resolver has targets
	full := `## Epic 1: Slice 0a — Reference

### Story 1.1: Pick BC
### Story 1.4: Verify task check

## Epic 4: Slice 1 — identity

### Story 4.1: Aggregates

### Story 4.2: Canonical Service

---
story_id: "4.2"
depends_on: ["4.1"]
complexity: medium
---

- **Given** Story 4.1 emits events
- **Given** Slice 0a helper available
`
	path = writeEpics(t, full)
	parsed, _ = inferdeps.ParseEpics(path)
	sugs := inferdeps.Resolve(parsed, false)

	patched, backupPath, skipped, err := inferdeps.ApplyPatches(path, sugs, true)
	if err != nil {
		t.Fatal(err)
	}
	if patched != 1 {
		t.Errorf("patched=%d, want 1", patched)
	}
	if backupPath == "" {
		t.Errorf("backup path should be set")
	}
	if len(skipped) != 0 {
		t.Errorf("skipped should be empty (story has frontmatter), got %v", skipped)
	}

	after, _ := os.ReadFile(path)
	if !strings.Contains(string(after), `depends_on: ["1.4", "4.1"]`) {
		t.Errorf("rewrite missing merged depends_on:\n%s", string(after))
	}
	// Verify backup is the original (with the old depends_on).
	back, _ := os.ReadFile(backupPath)
	if !strings.Contains(string(back), `depends_on: ["4.1"]`) {
		t.Errorf("backup should contain original line:\n%s", string(back))
	}
}

func TestApplyPatches_BlockListFormReplaced(t *testing.T) {
	t.Parallel()
	src := `## Epic 1: Slice 0a — Reference

### Story 1.4: Verify

## Epic 4: Slice 1 — identity

### Story 4.1: Aggregates

### Story 4.2: Canonical Service

---
story_id: "4.2"
depends_on:
  - "4.1"
complexity: medium
---

- **Given** Slice 0a helper available
`
	path := writeEpics(t, src)
	parsed, _ := inferdeps.ParseEpics(path)
	sugs := inferdeps.Resolve(parsed, false)

	patched, _, _, err := inferdeps.ApplyPatches(path, sugs, false)
	if err != nil {
		t.Fatal(err)
	}
	if patched != 1 {
		t.Errorf("patched=%d, want 1", patched)
	}
	after, _ := os.ReadFile(path)
	// Block-list collapsed into inline form post-apply (acceptable for v1).
	if !strings.Contains(string(after), `depends_on: ["1.4", "4.1"]`) {
		t.Errorf("rewrite missing merged list:\n%s", string(after))
	}
	// The old `- "4.1"` line must be gone.
	if strings.Contains(string(after), `  - "4.1"`) {
		t.Errorf("block-list residue should be removed:\n%s", string(after))
	}
	// complexity must still be present.
	if !strings.Contains(string(after), `complexity: medium`) {
		t.Errorf("apply mangled the rest of the frontmatter:\n%s", string(after))
	}
}

func TestFilterByEpic_KeepsOnlyMatchingStories(t *testing.T) {
	t.Parallel()
	sugs := []inferdeps.Suggestion{
		{StoryID: "1.1", EpicID: "1"},
		{StoryID: "4.1", EpicID: "4"},
		{StoryID: "4.2", EpicID: "4"},
		{StoryID: "10.3", EpicID: "10"},
	}
	out := inferdeps.FilterByEpic(sugs, "4")
	if len(out) != 2 || out[0].StoryID != "4.1" || out[1].StoryID != "4.2" {
		t.Errorf("filtered = %+v, want [4.1 4.2]", out)
	}
	// "" is no-op
	if len(inferdeps.FilterByEpic(sugs, "")) != 4 {
		t.Errorf("empty epic filter should be no-op")
	}
}
