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
}
