// Package sprint holds the v6 sprint-level use cases: epics.md parsing,
// dependency-graph batching, and the orchestrator entry-point glue.
package sprint

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/sosalejandro/bmad-story-runner-cli/domain/state"
)

// StoryFrontmatter mirrors the per-story yaml frontmatter inside an epics.md
// story section. Only fields meaningful at planning time are parsed.
type StoryFrontmatter struct {
	StoryID        string   `yaml:"story_id"`
	Title          string   `yaml:"title"`
	DependsOn      []string `yaml:"depends_on"`
	Affects        []string `yaml:"affects"`
	ResourceBudget *struct {
		RamMB    int     `yaml:"ram_mb"`
		CPUCores float64 `yaml:"cpu_cores"`
	} `yaml:"resource_budget"`
	RequiresAndroid bool   `yaml:"requires_android"`
	Complexity      string `yaml:"complexity"`
	StoryType       string `yaml:"story_type"` // doc | code | mixed (default: code)

	// BypassEpicRequires lets a single story opt out of one or more
	// epic-level `requires_epics:` entries inherited from its parent epic
	// header (issue #46). Rare; documented as an escape valve, not a
	// default. Values are epic ids (ints).
	BypassEpicRequires []int `yaml:"bypass_epic_requires"`
}

// EpicFrontmatter is the YAML block that may appear directly under a
// `## Epic N: <title>` header (issue #46). It declares cross-epic
// dependencies at the epic level so every story inside the epic implicitly
// depends on the last story of each referenced epic — no more linear-chain
// placeholders on first-story-of-epic-N.
//
// All fields are optional. An epic without an EpicFrontmatter block behaves
// exactly as it did pre-#46 (story-level `depends_on` is the only signal).
type EpicFrontmatter struct {
	// EpicID is the epic id. Optional; when omitted we use the integer
	// from the `## Epic N` header. Allowing an explicit value matches the
	// pattern story_id uses in StoryFrontmatter and is useful when an epic
	// gets renumbered without renaming the header (rare but documented).
	EpicID int `yaml:"epic_id"`

	// RequiresEpics expands to "every story in this epic implicitly
	// depends_on the LAST story of each referenced epic". Self-reference
	// is silently dropped (an epic requiring itself is a no-op).
	RequiresEpics []int `yaml:"requires_epics"`

	// RequiresStories pins extra cross-cutting story ids not captured by
	// epic-level granularity. These are added to every story in the epic.
	RequiresStories []string `yaml:"requires_stories"`

	// ProvidesToEpics is informational only — the inverse for visualization.
	// The planner ignores it (resolver builds the inverse from requires_epics
	// when callers need it). Kept here so authors don't get a yaml-unmarshal
	// surprise if they put it in the file.
	ProvidesToEpics []int `yaml:"provides_to_epics"`
}

// ParsedEpic carries the parsed `## Epic N` header + its (optional)
// frontmatter. Stored by ParseEpicsFileFull so the planner can synthesize
// cross-epic edges at ingest time.
type ParsedEpic struct {
	// EpicID is the canonical id of the epic — either from the YAML
	// frontmatter's `epic_id:` field or the integer captured by the
	// `## Epic N` header regex. Stored as a string to match the story-id
	// vocabulary used everywhere downstream (so "4" not 4).
	EpicID string

	// Title is the trailing portion of the `## Epic N: <title>` line.
	Title string

	// Frontmatter is the (optional) YAML block. Zero value means "no
	// requires_epics / no requires_stories" — the epic behaves as it did
	// pre-#46.
	Frontmatter EpicFrontmatter

	// HasFrontmatter is true iff a YAML `---` block was parsed for the
	// epic header. Lets the planner distinguish "operator hasn't filled
	// this in yet" from "operator explicitly chose no cross-epic deps".
	HasFrontmatter bool

	// LineNum is the 1-based line of the `## Epic N` header — surfaced in
	// warnings so an operator can jump straight to the offending header.
	LineNum int
}

// ParsedStory carries the frontmatter + the file path it was sourced from.
type ParsedStory struct {
	Frontmatter StoryFrontmatter
	SourceFile  string
	StoryTitle  string // markdown title (### Story X.Y: <title>); fallback for Frontmatter.Title
	// HasFrontmatter is true iff the parser saw a non-empty YAML block between
	// `---` delimiters for this story. Stories that fall through with just the
	// header-derived StoryID + Title (no yaml at all, or an empty `---\n---`)
	// are flagged false — the planner's coverage-warning surfaces these so an
	// operator can backfill dependencies before sprinting through them.
	HasFrontmatter bool

	// EpicID is the epic the story belongs to. Captured at parse time from
	// the most-recent `## Epic N` header in document order. Used by the
	// planner (issue #46) to attach inherited epic-level cross-epic deps.
	// Empty when the story precedes any epic header (defensive only —
	// real epics.md files always lead with `## Epic 1`).
	EpicID string
}

// ParsedEpicsFile is the full result of ParseEpicsFileFull — both the
// epic headers and the stories they contain, in document order. The
// planner consumes both to synthesize cross-epic edges (issue #46).
type ParsedEpicsFile struct {
	Epics   []ParsedEpic
	Stories []ParsedStory
}

var (
	// Story ID accepts any run of letters/digits/dot/dash/underscore — matches
	// both "4.1" and "1.1.payment-method-management" and slug-style ids.
	// The trailing `[:\-] <title>` is OPTIONAL — headers without an explicit
	// title (e.g. mid-draft `### Story 1.1` with the title still TBD) match
	// too. Authors editing real epics.md files routinely commit titleless
	// stubs while shaping the dependency graph; the planner must not lose
	// them.
	storyHeaderRE = regexp.MustCompile(`^###\s+Story\s+([\w.\-]+)\s*(?:[:\-]\s*(.+))?$`)

	// Epic ID currently restricted to a leading integer per the v6 spec
	// (`## Epic 1`, `## Epic 13`). Allowing slugs would invalidate the
	// "last story of epic N" ordinal computation downstream.
	epicHeaderRE = regexp.MustCompile(`^##\s+Epic\s+(\d+)\s*[:\-]?\s*(.*)$`)
)

// ParseEpicsFile reads an epics.md file and returns every story it contains.
//
// Back-compat wrapper around ParseEpicsFileFull — drops the epic-header
// metadata. Existing callers (the legacy tests + a few downstream tools)
// that only need the story list keep working unchanged. New code (the
// planner, issue #46) calls ParseEpicsFileFull directly to get the epic
// frontmatter alongside.
func ParseEpicsFile(path string) ([]ParsedStory, error) {
	parsed, err := ParseEpicsFileFull(path)
	if err != nil {
		return nil, err
	}
	return parsed.Stories, nil
}

// ParseEpicsFileFull reads an epics.md file and returns both the parsed
// epic headers (with their optional `requires_epics:` frontmatter, issue
// #46) and every story it contains.
//
// Grammar (per the v6 spec's epics.md layout):
//
//	## Epic N: <title>
//
//	---                          (optional, issue #46)
//	epic_id: N
//	requires_epics: [4, 6]
//	requires_stories: ["3.4"]
//	---
//
//	### Story <id>: <title>
//
//	---                          (existing, optional)
//	story_id: "..."
//	depends_on: [...]
//	...
//	---
//
//	(free-form body, ignored for planning)
//
// Multiple stories per file. Frontmatter (epic-level AND story-level) is
// OPTIONAL — items without it are still emitted (so a freshly-drafted epic
// doesn't silently disappear), with all dependency fields empty.
func ParseEpicsFileFull(path string) (ParsedEpicsFile, error) {
	f, err := os.Open(path)
	if err != nil {
		return ParsedEpicsFile{}, fmt.Errorf("open epics %s: %w", path, err)
	}
	defer f.Close()

	var (
		out          ParsedEpicsFile
		currentEpic  *ParsedEpic
		currentStory *ParsedStory
		inYAML       bool
		yamlBuf      strings.Builder
		// yamlOwner tracks which entity (`epic` | `story`) the in-progress
		// YAML block belongs to. The same `---` delimiter is reused at both
		// levels; the scanner picks the owner based on document state at the
		// moment we entered the block.
		yamlOwner string
		lineNum   int
	)

	flushStory := func() error {
		if currentStory == nil {
			return nil
		}
		if yamlOwner == "story" && yamlBuf.Len() > 0 {
			if err := commitStoryYAML(currentStory, &yamlBuf); err != nil {
				return err
			}
		}
		// Inherit parent epic id for the planner's cross-epic resolver.
		if currentEpic != nil {
			currentStory.EpicID = currentEpic.EpicID
		}
		out.Stories = append(out.Stories, *currentStory)
		currentStory = nil
		yamlBuf.Reset()
		yamlOwner = ""
		inYAML = false
		return nil
	}

	flushEpic := func() error {
		// Closing an epic also closes its open story (defensive — well-formed
		// epics.md files always have a Story between two `## Epic` headers).
		if err := flushStory(); err != nil {
			return err
		}
		if currentEpic == nil {
			return nil
		}
		if yamlOwner == "epic" && yamlBuf.Len() > 0 {
			if err := commitEpicYAML(currentEpic, &yamlBuf); err != nil {
				return err
			}
		}
		out.Epics = append(out.Epics, *currentEpic)
		currentEpic = nil
		yamlBuf.Reset()
		yamlOwner = ""
		inYAML = false
		return nil
	}

	scan := bufio.NewScanner(f)
	scan.Buffer(make([]byte, 1<<20), 1<<20) // allow long lines
	for scan.Scan() {
		lineNum++
		line := scan.Text()

		if m := epicHeaderRE.FindStringSubmatch(line); m != nil {
			if err := flushEpic(); err != nil {
				return ParsedEpicsFile{}, fmt.Errorf("parse %s: %w", path, err)
			}
			currentEpic = &ParsedEpic{
				EpicID:  m[1],
				Title:   strings.TrimSpace(m[2]),
				LineNum: lineNum,
			}
			// Reset YAML state — a `---` block following the epic header
			// belongs to the epic, not to any prior story.
			inYAML = false
			yamlBuf.Reset()
			yamlOwner = ""
			continue
		}

		if m := storyHeaderRE.FindStringSubmatch(line); m != nil {
			// A story header closes any in-progress story (and any open epic
			// YAML block that never got a closing `---` — defensive).
			if err := flushStory(); err != nil {
				return ParsedEpicsFile{}, fmt.Errorf("parse %s: %w", path, err)
			}
			// If we were mid-way through an epic YAML block when the story
			// header arrived, commit it now — story body takes precedence.
			if yamlOwner == "epic" && yamlBuf.Len() > 0 && currentEpic != nil {
				if err := commitEpicYAML(currentEpic, &yamlBuf); err != nil {
					return ParsedEpicsFile{}, fmt.Errorf("parse %s epic %s: %w", path, currentEpic.EpicID, err)
				}
				yamlBuf.Reset()
				yamlOwner = ""
				inYAML = false
			}
			currentStory = &ParsedStory{
				SourceFile:  path,
				StoryTitle:  strings.TrimSpace(m[2]),
				Frontmatter: StoryFrontmatter{StoryID: m[1], Title: strings.TrimSpace(m[2])},
			}
			inYAML = false
			yamlBuf.Reset()
			yamlOwner = ""
			continue
		}

		// `---` is a YAML fence. Ownership picks who's currently open:
		// a fresh `---` after `## Epic` (and BEFORE any `### Story`)
		// belongs to the epic. A `---` after `### Story` belongs to the
		// story.
		if strings.TrimSpace(line) == "---" {
			if !inYAML {
				// Entering YAML. Pick the owner based on document state.
				if currentStory != nil {
					yamlOwner = "story"
				} else if currentEpic != nil {
					yamlOwner = "epic"
				} else {
					// Free-floating `---` before any header — treat as
					// no-op (covers the markdown title-front-matter pattern
					// some authors keep at the very top of the file).
					continue
				}
				inYAML = true
			} else {
				// Exiting YAML. Commit the buffer to whoever owns it.
				switch yamlOwner {
				case "story":
					if currentStory != nil {
						if err := commitStoryYAML(currentStory, &yamlBuf); err != nil {
							return ParsedEpicsFile{}, fmt.Errorf("parse %s story %s: %w", path, currentStory.Frontmatter.StoryID, err)
						}
					}
				case "epic":
					if currentEpic != nil {
						if err := commitEpicYAML(currentEpic, &yamlBuf); err != nil {
							return ParsedEpicsFile{}, fmt.Errorf("parse %s epic %s: %w", path, currentEpic.EpicID, err)
						}
					}
				}
				yamlBuf.Reset()
				yamlOwner = ""
				inYAML = false
			}
			continue
		}

		if inYAML {
			yamlBuf.WriteString(line)
			yamlBuf.WriteString("\n")
		}
	}
	if err := scan.Err(); err != nil {
		return ParsedEpicsFile{}, fmt.Errorf("scan %s: %w", path, err)
	}
	// EOF — close any in-flight story/epic.
	if err := flushEpic(); err != nil {
		return ParsedEpicsFile{}, fmt.Errorf("parse %s: %w", path, err)
	}

	return out, nil
}

// commitStoryYAML merges any buffered yaml into the current story's frontmatter.
func commitStoryYAML(s *ParsedStory, buf *strings.Builder) error {
	if buf.Len() == 0 {
		return nil
	}
	var fm StoryFrontmatter
	if err := yaml.Unmarshal([]byte(buf.String()), &fm); err != nil {
		return fmt.Errorf("yaml parse: %w", err)
	}
	// Preserve markdown-header-derived id/title when frontmatter omits them.
	if fm.StoryID == "" {
		fm.StoryID = s.Frontmatter.StoryID
	}
	if fm.Title == "" {
		fm.Title = s.StoryTitle
	}
	s.Frontmatter = fm
	s.HasFrontmatter = true
	return nil
}

// commitEpicYAML merges any buffered yaml into the current epic header.
// Empty buffer is a no-op (the parser may flush a zero-length block when
// the epic has the `---\n---` shell but no body — we treat that as "no
// frontmatter", same as for stories).
func commitEpicYAML(e *ParsedEpic, buf *strings.Builder) error {
	if buf.Len() == 0 {
		return nil
	}
	var fm EpicFrontmatter
	if err := yaml.Unmarshal([]byte(buf.String()), &fm); err != nil {
		return fmt.Errorf("yaml parse: %w", err)
	}
	// If frontmatter omitted epic_id, fall back to the header-derived value.
	if fm.EpicID == 0 {
		if n, err := strconv.Atoi(e.EpicID); err == nil {
			fm.EpicID = n
		}
	}
	e.Frontmatter = fm
	e.HasFrontmatter = true
	return nil
}

// EpicIDFromStory returns the epic-id prefix for a story id, e.g. "1.1" → "1",
// "10.4" → "10", "4.1.payment-method-mgmt" → "4". Returns the input unchanged
// when no dot is present (the caller can decide what that means).
func EpicIDFromStory(storyID string) string {
	if i := strings.IndexByte(storyID, '.'); i >= 0 {
		return storyID[:i]
	}
	return storyID
}

// StoryMatchesEpic returns true when storyID is in the scope of epicID:
//
//	"1"    matches "1.1", "1.2"   (prefix + dot)
//	"1"    matches "1"            (exact equality)
//	"1"    does NOT match "10.1"  (avoids the off-by-one prefix bug)
//	""     matches everything (an empty scope is a no-op filter)
func StoryMatchesEpic(storyID, epicID string) bool {
	if epicID == "" {
		return true
	}
	if storyID == epicID {
		return true
	}
	return strings.HasPrefix(storyID, epicID+".")
}

// ToStory converts a parsed-story to a domain.Story ready for Stories.Insert.
func (p ParsedStory) ToStory() state.Story {
	complexity := state.Complexity(p.Frontmatter.Complexity)
	if complexity == "" {
		complexity = state.ComplexityMedium
	}
	storyType := state.StoryType(p.Frontmatter.StoryType)
	if storyType == "" {
		storyType = state.StoryTypeCode
	}
	title := p.Frontmatter.Title
	if title == "" {
		title = p.StoryTitle
	}
	st := state.Story{
		ID:              p.Frontmatter.StoryID,
		File:            p.SourceFile,
		Title:           title,
		Status:          state.StatusPending,
		Complexity:      complexity,
		StoryType:       storyType,
		RequiresAndroid: p.Frontmatter.RequiresAndroid,
	}
	if p.Frontmatter.ResourceBudget != nil {
		st.ResourceBudget = &state.ResourceBudget{
			RamMB:    p.Frontmatter.ResourceBudget.RamMB,
			CPUCores: p.Frontmatter.ResourceBudget.CPUCores,
		}
	}
	return st
}
