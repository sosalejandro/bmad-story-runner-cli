package sprint_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sosalejandro/bmad-story-runner-cli/application/sprint"
)

const sampleEpics = `# Sample Epics

## Epic 1: Foundation

### Story 1.1: Choose Reference-BC Candidate

As a developer agent, I want to pick a canonical BC...

- **Given** the BC inventory
- **When** I pick
- **Then** the choice is ` + "`measurements`" + `
- **Refs:** FR-Arch-7

### Story 1.2: Document Helper Pattern

- **Refs:** FR-Arch-2, FR-Arch-13

## Epic 2: More Stuff

### Story 2.1: Something else
`

const sampleArch = `# Architecture

## Cross-cutting

Some context here.

### FR-Arch-7 — Reference BC Walkthrough

This is the architecture section for FR-Arch-7. It contains the canonical
pattern that Story 1.1 references.

### FR-Arch-2 — saveWithEvents Helper

Body of the FR-Arch-2 section.

### FR-Arch-13 — Walkthrough Doc Convention

Body of FR-Arch-13.

## Some Other Section

This should NOT be in any extract.
`

func TestBuildStoryContext_ExtractsEntryAndArchSections(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	epicsPath := filepath.Join(dir, "epics.md")
	archPath := filepath.Join(dir, "arch.md")
	outPath := filepath.Join(dir, "out", "1.2.md")

	if err := os.WriteFile(epicsPath, []byte(sampleEpics), 0o644); err != nil {
		t.Fatalf("write epics: %v", err)
	}
	if err := os.WriteFile(archPath, []byte(sampleArch), 0o644); err != nil {
		t.Fatalf("write arch: %v", err)
	}

	res, err := sprint.BuildStoryContext(outPath, sprint.StoryContextSources{
		StoryID:          "1.2",
		EpicsPath:        epicsPath,
		ArchitecturePath: archPath,
	})
	if err != nil {
		t.Fatalf("BuildStoryContext: %v", err)
	}

	body, _ := os.ReadFile(outPath)
	got := string(body)

	// Entry section
	if !strings.Contains(got, "### Story 1.2: Document Helper Pattern") {
		t.Errorf("missing story 1.2 entry\n--- bundle ---\n%s", got)
	}
	// Both FR refs from the entry should pull their arch sections
	if !strings.Contains(got, "FR-Arch-2 — saveWithEvents Helper") {
		t.Errorf("missing FR-Arch-2 arch section")
	}
	if !strings.Contains(got, "FR-Arch-13 — Walkthrough Doc Convention") {
		t.Errorf("missing FR-Arch-13 arch section")
	}
	// FR-Arch-7 was NOT in 1.2's entry; should be ABSENT
	if strings.Contains(got, "FR-Arch-7 — Reference BC Walkthrough") {
		t.Errorf("FR-Arch-7 should not be in 1.2's bundle (not referenced)")
	}
	// "Some Other Section" should not leak
	if strings.Contains(got, "Some Other Section") {
		t.Errorf("unrelated arch section leaked into bundle")
	}

	if len(res.Sections) < 2 {
		t.Errorf("res.Sections = %v, want at least entry + 1 arch section", res.Sections)
	}
}

func TestBuildStoryContext_PrefixMatchDoesntCollide(t *testing.T) {
	t.Parallel()
	// Guard against the "looking for Story 1.1 grabs Story 1.10" foot-gun.
	dir := t.TempDir()
	epicsPath := filepath.Join(dir, "epics.md")
	_ = os.WriteFile(epicsPath, []byte(`
### Story 1.1: First

Real 1.1.

### Story 1.10: Decimal

Should NOT be returned when asking for 1.1.

### Story 1.11: Eleven

Also should NOT.
`), 0o644)

	res, err := sprint.BuildStoryContext(
		filepath.Join(dir, "out.md"),
		sprint.StoryContextSources{StoryID: "1.1", EpicsPath: epicsPath},
	)
	if err != nil {
		t.Fatalf("BuildStoryContext: %v", err)
	}
	body, _ := os.ReadFile(res.OutPath)
	got := string(body)
	if !strings.Contains(got, "Story 1.1:") {
		t.Fatalf("missing real 1.1")
	}
	if strings.Contains(got, "Story 1.10:") || strings.Contains(got, "Story 1.11:") {
		t.Fatalf("prefix match collision: bundle contains 1.10 or 1.11\n%s", got)
	}
}

func TestBuildStoryContext_StoryNotFound(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	epicsPath := filepath.Join(dir, "epics.md")
	_ = os.WriteFile(epicsPath, []byte(sampleEpics), 0o644)

	_, err := sprint.BuildStoryContext(
		filepath.Join(dir, "out.md"),
		sprint.StoryContextSources{StoryID: "99.99", EpicsPath: epicsPath},
	)
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not-found error, got %v", err)
	}
}

func TestBuildStoryContext_NoArchPathSkipsExtraction(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	epicsPath := filepath.Join(dir, "epics.md")
	_ = os.WriteFile(epicsPath, []byte(sampleEpics), 0o644)

	res, err := sprint.BuildStoryContext(
		filepath.Join(dir, "out.md"),
		sprint.StoryContextSources{StoryID: "1.1", EpicsPath: epicsPath},
	)
	if err != nil {
		t.Fatalf("BuildStoryContext: %v", err)
	}
	if len(res.Warnings) == 0 {
		t.Errorf("expected warning about missing arch path, got none")
	}
}
