// Package inferdeps parses epics.md prose for dependency cues (**Given**,
// **Refs**, epic-level prose) and turns them into suggested `depends_on:`
// YAML patches per story.
//
// The package is deterministic and read-only: it never mutates the source
// epics.md file unless `--apply` is passed via the cobra wrapper, and even
// then the mutation is a targeted YAML-frontmatter rewrite per story.
package inferdeps

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strings"
)

// EpicHeader captures the `## Epic N: Slice <s> — <name>` line metadata.
// We track the slice tag (e.g. "0a", "1", "13") because **Given** prose
// commonly references "Slice 0a" rather than "Epic 1".
type EpicHeader struct {
	EpicID  string // "1", "10", "22"
	Slice   string // "0a", "1", "13" — empty when header has no "Slice <x>" tag
	Name    string // the human-readable trailing name
	LineNum int    // 1-based line in source
}

// StoryProse holds the per-story raw prose cues the resolver consumes.
// Only fields meaningful for dependency inference are populated; the
// original sprint.ParseEpicsFile handles full frontmatter parsing.
type StoryProse struct {
	StoryID string // "4.2"
	Title   string // "Identity Canonical Service Shape"
	EpicID  string // the epic header in effect when this story was parsed
	Slice   string // ditto — Slice tag from the parent epic, may be empty
	LineNum int    // 1-based line where `### Story X.Y` appeared

	// FrontmatterDependsOn is the depends_on list already declared in the
	// frontmatter (if any). When the resolver suggests deps, this lets the
	// patch emitter distinguish "new" vs "already-present" cues, and the
	// validation harness compute an agreement rate.
	FrontmatterDependsOn []string

	// HasFrontmatter mirrors sprint.ParsedStory.HasFrontmatter so callers
	// can decide whether to emit a full frontmatter block (no existing
	// frontmatter) vs. an in-place depends_on patch.
	HasFrontmatter bool

	// GivenLines are the raw text after the `**Given**` marker on each
	// matching line, with the marker + bullet prefix stripped. One entry
	// per `**Given**` cue (most stories have exactly one).
	GivenLines []string

	// RefsLines are the raw text after `**Refs:**`. Most stories have one.
	RefsLines []string
}

// ParsedEpicsProse is the full result of parsing one epics.md file. Epics
// is kept (rather than only stories) so the resolver can answer "which
// epic owns slice 0c?" without re-walking.
type ParsedEpicsProse struct {
	Epics   []EpicHeader
	Stories []StoryProse
}

var (
	epicHeaderRE  = regexp.MustCompile(`^##\s+Epic\s+(\d+)\s*[:\-]\s*(.+)$`)
	sliceTagRE    = regexp.MustCompile(`Slice\s+([0-9]+[a-z]?)`)
	storyHeaderRE = regexp.MustCompile(`^###\s+Story\s+([\w.\-]+)\s*[:\-]\s*(.+)$`)

	// `- **Given** ...`, `**Given** ...`, `- **Given:** ...`, etc.
	givenLineRE = regexp.MustCompile(`^\s*(?:-\s+)?\*\*Given\*\*[:\s]*(.+)$`)
	refsLineRE  = regexp.MustCompile(`^\s*(?:-\s+)?\*\*Refs:?\*\*[:\s]*(.+)$`)

	dependsOnLineRE = regexp.MustCompile(`^\s*depends_on\s*:\s*\[(.*)\]\s*$`)
	yamlItemRE      = regexp.MustCompile(`"([^"]+)"|'([^']+)'|([^\s,]+)`)
)

// ParseEpics reads an epics.md file and extracts every epic header + every
// story body's `**Given**` / `**Refs**` cues. The output is intentionally
// minimal — downstream resolution lives in resolver.go.
func ParseEpics(path string) (*ParsedEpicsProse, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("inferdeps: open %s: %w", path, err)
	}
	defer f.Close()

	out := &ParsedEpicsProse{}

	var (
		currentEpic    *EpicHeader
		currentStory   *StoryProse
		inYAML         bool
		yamlBuf        strings.Builder
		lineNum        int
	)

	flushStory := func() {
		if currentStory == nil {
			return
		}
		// Persist any frontmatter-derived depends_on the resolver should
		// honor as "already declared".
		if yamlBuf.Len() > 0 {
			currentStory.FrontmatterDependsOn = extractFrontmatterDependsOn(yamlBuf.String())
			currentStory.HasFrontmatter = true
		}
		out.Stories = append(out.Stories, *currentStory)
		currentStory = nil
		yamlBuf.Reset()
		inYAML = false
	}

	scan := bufio.NewScanner(f)
	scan.Buffer(make([]byte, 1<<20), 1<<20)
	for scan.Scan() {
		lineNum++
		line := scan.Text()

		// Epic header
		if m := epicHeaderRE.FindStringSubmatch(line); m != nil {
			flushStory()
			eh := EpicHeader{
				EpicID:  m[1],
				Name:    strings.TrimSpace(m[2]),
				LineNum: lineNum,
			}
			if sm := sliceTagRE.FindStringSubmatch(eh.Name); sm != nil {
				eh.Slice = sm[1]
			}
			out.Epics = append(out.Epics, eh)
			currentEpic = &out.Epics[len(out.Epics)-1]
			continue
		}

		// Story header
		if m := storyHeaderRE.FindStringSubmatch(line); m != nil {
			flushStory()
			s := StoryProse{
				StoryID: m[1],
				Title:   strings.TrimSpace(m[2]),
				LineNum: lineNum,
			}
			if currentEpic != nil {
				s.EpicID = currentEpic.EpicID
				s.Slice = currentEpic.Slice
			}
			currentStory = &s
			continue
		}

		if currentStory == nil {
			continue
		}

		// YAML frontmatter detection — collect the body verbatim so we
		// can pull depends_on out of it later.
		if strings.TrimSpace(line) == "---" {
			inYAML = !inYAML
			continue
		}
		if inYAML {
			yamlBuf.WriteString(line)
			yamlBuf.WriteString("\n")
			continue
		}

		// `**Given** ...` cue
		if m := givenLineRE.FindStringSubmatch(line); m != nil {
			currentStory.GivenLines = append(currentStory.GivenLines, strings.TrimSpace(m[1]))
			continue
		}
		// `**Refs:** ...` cue
		if m := refsLineRE.FindStringSubmatch(line); m != nil {
			currentStory.RefsLines = append(currentStory.RefsLines, strings.TrimSpace(m[1]))
			continue
		}
	}
	if err := scan.Err(); err != nil {
		return nil, fmt.Errorf("inferdeps: scan %s: %w", path, err)
	}
	flushStory()
	return out, nil
}

// extractFrontmatterDependsOn pulls the depends_on list out of a YAML
// frontmatter block. We deliberately do NOT use a full YAML decoder here
// — the inferred-deps tool only cares about the depends_on shape, and a
// hand-rolled extractor is bulletproof against the messy free-form
// frontmatter shapes the nutrition team uses in practice (e.g. trailing
// commas, mixed quote styles, multi-line block lists).
func extractFrontmatterDependsOn(yamlBody string) []string {
	scan := bufio.NewScanner(strings.NewReader(yamlBody))
	var (
		out         []string
		inBlockList bool
	)
	for scan.Scan() {
		raw := scan.Text()
		trim := strings.TrimSpace(raw)

		// Inline form: depends_on: ["1.1", "2.3"]
		if m := dependsOnLineRE.FindStringSubmatch(raw); m != nil {
			for _, item := range yamlItemRE.FindAllStringSubmatch(m[1], -1) {
				val := firstNonEmpty(item[1], item[2], item[3])
				if val != "" {
					out = append(out, val)
				}
			}
			inBlockList = false
			continue
		}

		// Block-list form:
		//   depends_on:
		//     - "1.1"
		//     - "2.3"
		if strings.HasPrefix(trim, "depends_on:") && !strings.Contains(trim, "[") {
			inBlockList = true
			continue
		}

		if inBlockList {
			if strings.HasPrefix(trim, "- ") {
				item := strings.TrimPrefix(trim, "- ")
				item = strings.Trim(item, `"'`)
				if item != "" {
					out = append(out, item)
				}
				continue
			}
			// Any non-list-item line ends the block.
			inBlockList = false
		}
	}
	return out
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}
