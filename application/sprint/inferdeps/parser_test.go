package inferdeps_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sosalejandro/bmad-story-runner-cli/application/sprint/inferdeps"
)

func writeEpics(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "epics.md")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("write epics: %v", err)
	}
	return p
}

func TestParseEpics_CapturesGivenAndRefs(t *testing.T) {
	t.Parallel()
	path := writeEpics(t, `## Epic 1: Slice 0a — Canonical Reference

### Story 1.1: Pick Reference BC

---
story_id: "1.1"
depends_on: []
---

- **Given** the 27-BC inventory
- **When** I pick a candidate
- **Then** the choice is documented
- **Refs:** FR-Arch-7

### Story 1.2: Document Helper

---
story_id: "1.2"
depends_on: ["1.1"]
---

- **Given** measurements BC chosen as reference
- **Refs:** FR-Arch-2
`)
	parsed, err := inferdeps.ParseEpics(path)
	if err != nil {
		t.Fatalf("ParseEpics: %v", err)
	}
	if len(parsed.Epics) != 1 {
		t.Fatalf("Epics=%d, want 1", len(parsed.Epics))
	}
	if parsed.Epics[0].EpicID != "1" || parsed.Epics[0].Slice != "0a" {
		t.Errorf("epic[0]=%+v, want EpicID=1 Slice=0a", parsed.Epics[0])
	}
	if len(parsed.Stories) != 2 {
		t.Fatalf("Stories=%d, want 2", len(parsed.Stories))
	}
	s := parsed.Stories[0]
	if s.StoryID != "1.1" || s.Title != "Pick Reference BC" {
		t.Errorf("story[0]=%+v, want 1.1", s)
	}
	if len(s.GivenLines) != 1 || s.GivenLines[0] != "the 27-BC inventory" {
		t.Errorf("Given=%v", s.GivenLines)
	}
	if len(s.RefsLines) != 1 || s.RefsLines[0] != "FR-Arch-7" {
		t.Errorf("Refs=%v", s.RefsLines)
	}
	if s.EpicID != "1" || s.Slice != "0a" {
		t.Errorf("inherited epic ctx = %+v", s)
	}

	s2 := parsed.Stories[1]
	if len(s2.FrontmatterDependsOn) != 1 || s2.FrontmatterDependsOn[0] != "1.1" {
		t.Errorf("FrontmatterDependsOn = %v", s2.FrontmatterDependsOn)
	}
	if !s2.HasFrontmatter {
		t.Errorf("HasFrontmatter should be true")
	}
}

func TestParseEpics_BlockListDependsOn(t *testing.T) {
	t.Parallel()
	path := writeEpics(t, `## Epic 4: Slice 1 — identity BC

### Story 4.3: Identity *WithID Methods

---
story_id: "4.3"
depends_on:
  - "4.2"
  - "3.6"
complexity: low
---

- **Given** canonical service in place
- **Refs:** FR-Arch-2
`)
	parsed, err := inferdeps.ParseEpics(path)
	if err != nil {
		t.Fatalf("ParseEpics: %v", err)
	}
	if len(parsed.Stories) != 1 {
		t.Fatalf("Stories=%d", len(parsed.Stories))
	}
	got := parsed.Stories[0].FrontmatterDependsOn
	if len(got) != 2 || got[0] != "4.2" || got[1] != "3.6" {
		t.Errorf("FrontmatterDependsOn=%v, want [4.2 3.6]", got)
	}
}

func TestParseEpics_StoryWithoutFrontmatter(t *testing.T) {
	t.Parallel()
	path := writeEpics(t, `## Epic 2: Slice 0b — Scaffolds

### Story 2.1: Lint Config

As a developer agent, I want lint enabled.

- **Given** existing baseline
- **Refs:** NFR-1
`)
	parsed, err := inferdeps.ParseEpics(path)
	if err != nil {
		t.Fatalf("ParseEpics: %v", err)
	}
	if len(parsed.Stories) != 1 {
		t.Fatalf("Stories=%d", len(parsed.Stories))
	}
	if parsed.Stories[0].HasFrontmatter {
		t.Errorf("HasFrontmatter should be false")
	}
	if len(parsed.Stories[0].GivenLines) != 1 {
		t.Errorf("Given=%v", parsed.Stories[0].GivenLines)
	}
}

func TestParseEpics_MultipleGivenLines(t *testing.T) {
	t.Parallel()
	path := writeEpics(t, `## Epic 1: Slice 0a — Ref

### Story 1.1: Multi-Given

- **Given** prerequisite A
- **Given** prerequisite B
- **Refs:** FR-1, FR-2
`)
	parsed, err := inferdeps.ParseEpics(path)
	if err != nil {
		t.Fatalf("ParseEpics: %v", err)
	}
	if len(parsed.Stories[0].GivenLines) != 2 {
		t.Errorf("Given=%v want 2 entries", parsed.Stories[0].GivenLines)
	}
}
