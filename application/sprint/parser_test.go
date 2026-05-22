package sprint_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sosalejandro/bmad-story-runner-cli/application/sprint"
)

func writeEpics(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "epics.md")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write epics: %v", err)
	}
	return path
}

func TestParseEpicsFile_StoryWithFullFrontmatter(t *testing.T) {
	t.Parallel()
	path := writeEpics(t, `# Epic 4 — Identity

### Story 4.1: Identity Aggregates

---
story_id: "4.1"
depends_on: ["3.1", "3.2"]
affects:
  - src/identity/
  - src/shared/
resource_budget:
  ram_mb: 800
  cpu_cores: 0.6
requires_android: false
complexity: high
---

Body explaining the slice...
`)
	stories, err := sprint.ParseEpicsFile(path)
	if err != nil {
		t.Fatalf("ParseEpicsFile: %v", err)
	}
	if len(stories) != 1 {
		t.Fatalf("len = %d, want 1", len(stories))
	}
	s := stories[0].Frontmatter
	if s.StoryID != "4.1" {
		t.Errorf("StoryID = %q, want 4.1", s.StoryID)
	}
	if len(s.DependsOn) != 2 {
		t.Errorf("DependsOn = %v, want 2 entries", s.DependsOn)
	}
	if s.Complexity != "high" {
		t.Errorf("Complexity = %q, want high", s.Complexity)
	}
	if s.ResourceBudget == nil || s.ResourceBudget.RamMB != 800 {
		t.Errorf("ResourceBudget = %+v, want ram_mb=800", s.ResourceBudget)
	}
}

func TestParseEpicsFile_MultipleStories(t *testing.T) {
	t.Parallel()
	path := writeEpics(t, `### Story 1.1: Foo

---
story_id: "1.1"
---

### Story 1.2: Bar

---
story_id: "1.2"
depends_on: ["1.1"]
---
`)
	stories, _ := sprint.ParseEpicsFile(path)
	if len(stories) != 2 {
		t.Fatalf("len = %d, want 2", len(stories))
	}
	if stories[1].Frontmatter.DependsOn[0] != "1.1" {
		t.Errorf("second story deps wrong: %v", stories[1].Frontmatter.DependsOn)
	}
}

func TestParseEpicsFile_StoryWithoutFrontmatterStillEmitted(t *testing.T) {
	t.Parallel()
	path := writeEpics(t, `### Story 9.9: TBD

Body without frontmatter — should still parse to a stub story.
`)
	stories, err := sprint.ParseEpicsFile(path)
	if err != nil {
		t.Fatalf("ParseEpicsFile: %v", err)
	}
	if len(stories) != 1 {
		t.Fatalf("len = %d, want 1 stub", len(stories))
	}
	if stories[0].Frontmatter.StoryID != "9.9" {
		t.Errorf("StoryID = %q, want 9.9 from header", stories[0].Frontmatter.StoryID)
	}
	if stories[0].HasFrontmatter {
		t.Errorf("HasFrontmatter = true; want false for a header-only story (issue #14)")
	}
}

// HasFrontmatter must be true when YAML was actually parsed — this lets the
// coverage warning distinguish "no frontmatter" from "frontmatter present but
// depends_on empty (top-level on purpose)". Issue #14.
func TestParseEpicsFile_HasFrontmatterFlagSetOnYAML(t *testing.T) {
	t.Parallel()
	path := writeEpics(t, `### Story 1.1: With FM

---
story_id: "1.1"
depends_on: []
---

### Story 9.9: Without FM
`)
	stories, _ := sprint.ParseEpicsFile(path)
	if len(stories) != 2 {
		t.Fatalf("len = %d, want 2", len(stories))
	}
	if !stories[0].HasFrontmatter {
		t.Errorf("story 1.1: HasFrontmatter = false; want true (YAML block present)")
	}
	if stories[1].HasFrontmatter {
		t.Errorf("story 9.9: HasFrontmatter = true; want false (no YAML)")
	}
}

// TestParseEpicsFile_TolerateWorkflowFrontmatterAndTrailingHRules is a
// regression test for issue #58 — v0.3.0's ParseEpicsFileFull rejected a real
// 190-story epics.md file with:
//
//	yaml parse: yaml: line 4: did not find expected alphabetic or numeric character
//
// Two patterns must be tolerated:
//
//  1. **File-level top frontmatter** above the first `## Epic N` header. Some
//     workflow tools (e.g. bmad-bmm) prepend a yaml block with metadata like
//     `stepsCompleted`, `completedAt`, `inputDocuments`. Those keys don't
//     correspond to EpicFrontmatter / StoryFrontmatter fields and may contain
//     types (quoted dates, multi-line strings, nested lists) the typed structs
//     would choke on. The parser must skip the block, not try to unmarshal it.
//
//  2. **Horizontal-rule `---` lines between sections inside the document body**
//     (after a story's frontmatter has closed, e.g. before a trailing summary
//     section). Each entity (epic / story) accepts AT MOST ONE frontmatter
//     block — the one immediately following its header. Subsequent `---` lines
//     are markdown horizontal rules and must not re-open another YAML buffer
//     that greedily slurps body text into yaml.Unmarshal.
//
// Both shapes co-exist in the real nutrition-v2-go EDA cutover epics.md.
func TestParseEpicsFile_TolerateWorkflowFrontmatterAndTrailingHRules(t *testing.T) {
	t.Parallel()
	path := writeEpics(t, `---
stepsCompleted: [1, 2, 3, 4]
status: 'complete'
completedAt: '2026-05-16'
inputDocuments:
  - foo/architecture.md
  - foo/decision-tree.md
prdSubstitution: |
  No formal PRD. This brownfield refactor uses the decision tree + audit as
  PRD-equivalent inputs.
project_name: 'demo (cutover)'
---

# demo — Epic Breakdown

## Overview

Some prose between the top frontmatter and the first epic header.

## Epic 1: First Slice

### Story 1.1: Hello World

---
story_id: "1.1"
depends_on: []
complexity: low
---

As a developer agent, I want a hello story, So that the parser is exercised.

- **Given** the file
- **When** the parser runs
- **Then** the story is emitted

---

## Final Validation Summary

**Status:** Complete.

| Category    | Result            |
| ----------- | ----------------- |
| FR Coverage | All mapped        |
| Story Count | 1 story / 1 epic  |

**Total story count:** 1 story across 1 epic.
`)
	parsed, err := sprint.ParseEpicsFileFull(path)
	if err != nil {
		t.Fatalf("ParseEpicsFileFull rejected workflow-frontmatter + trailing HR: %v", err)
	}
	if len(parsed.Epics) != 1 {
		t.Errorf("epics = %d, want 1", len(parsed.Epics))
	}
	if len(parsed.Stories) != 1 {
		t.Fatalf("stories = %d, want 1", len(parsed.Stories))
	}
	if parsed.Stories[0].Frontmatter.StoryID != "1.1" {
		t.Errorf("StoryID = %q, want 1.1", parsed.Stories[0].Frontmatter.StoryID)
	}
	if !parsed.Stories[0].HasFrontmatter {
		t.Errorf("HasFrontmatter = false; want true (story 1.1 has a yaml block)")
	}
	if parsed.Stories[0].Frontmatter.Complexity != "low" {
		t.Errorf("Complexity = %q, want low (frontmatter must still be parsed correctly)",
			parsed.Stories[0].Frontmatter.Complexity)
	}
}

func TestEpicIDFromStory(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ in, want string }{
		{"1.1", "1"},
		{"10.4", "10"},
		{"1", "1"},                                  // no dot → return input
		{"4.1.payment-method-mgmt", "4"},            // multi-dot
		{"plans-patient.export-pdf", "plans-patient"}, // slug id
	} {
		if got := sprint.EpicIDFromStory(tc.in); got != tc.want {
			t.Errorf("EpicIDFromStory(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestStoryMatchesEpic_PrefixDotBoundary(t *testing.T) {
	t.Parallel()
	// The "1" vs "10" off-by-one prefix bug: a naive HasPrefix("10.1", "1")
	// would match incorrectly. We require either equality or "<epic>.".
	cases := []struct {
		story, epic string
		want        bool
	}{
		{"1.1", "1", true},
		{"1.2", "1", true},
		{"1", "1", true},
		{"10.1", "1", false}, // critical: must NOT match
		{"10.1", "10", true},
		{"2.1", "1", false},
		{"4.1.payment-method-mgmt", "4", true},
		{"x.1", "", true}, // empty scope = no filter
	}
	for _, tc := range cases {
		if got := sprint.StoryMatchesEpic(tc.story, tc.epic); got != tc.want {
			t.Errorf("StoryMatchesEpic(%q, %q) = %v, want %v", tc.story, tc.epic, got, tc.want)
		}
	}
}

func TestAnalyseCoverage_FlagsUnannotated(t *testing.T) {
	t.Parallel()
	parsed := []sprint.ParsedStory{
		{Frontmatter: sprint.StoryFrontmatter{StoryID: "1.1"}, HasFrontmatter: true},
		{Frontmatter: sprint.StoryFrontmatter{StoryID: "1.2"}, HasFrontmatter: true},
		{Frontmatter: sprint.StoryFrontmatter{StoryID: "10.1"}, HasFrontmatter: false},
		{Frontmatter: sprint.StoryFrontmatter{StoryID: "10.2"}, HasFrontmatter: false},
	}
	r := sprint.AnalyseCoverage(parsed)
	if r.TotalStories != 4 || r.WithFrontmatter != 2 || r.WithoutFrontmatter != 2 {
		t.Fatalf("coverage = %+v, want 4/2/2", r)
	}
	if len(r.UnannotatedStoryIDs) != 2 || r.UnannotatedStoryIDs[0] != "10.1" {
		t.Errorf("UnannotatedStoryIDs = %v", r.UnannotatedStoryIDs)
	}
}

func TestFilterByScope_RestrictsToOneEpic(t *testing.T) {
	t.Parallel()
	parsed := []sprint.ParsedStory{
		{Frontmatter: sprint.StoryFrontmatter{StoryID: "1.1"}},
		{Frontmatter: sprint.StoryFrontmatter{StoryID: "1.2"}},
		{Frontmatter: sprint.StoryFrontmatter{StoryID: "10.1"}}, // off-by-one trap
		{Frontmatter: sprint.StoryFrontmatter{StoryID: "2.1"}},
	}
	got := sprint.FilterByScope(parsed, "1")
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2 (1.1 + 1.2 only)", len(got))
	}
	if got[0].Frontmatter.StoryID != "1.1" || got[1].Frontmatter.StoryID != "1.2" {
		t.Errorf("filtered ids = %q,%q; want 1.1,1.2",
			got[0].Frontmatter.StoryID, got[1].Frontmatter.StoryID)
	}
	// Empty scope leaves slice intact.
	if all := sprint.FilterByScope(parsed, ""); len(all) != 4 {
		t.Errorf("empty scope dropped stories: got %d, want 4", len(all))
	}
}
