// Package sprint holds the v6 sprint-level use cases: epics.md parsing,
// dependency-graph batching, and the orchestrator entry-point glue.
package sprint

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/sosalejandro/bmad-story-runner-cli/domain/state"
)

// StoryFrontmatter mirrors the per-story yaml frontmatter inside an epics.md
// story section. Only fields meaningful at planning time are parsed.
type StoryFrontmatter struct {
	StoryID         string   `yaml:"story_id"`
	Title           string   `yaml:"title"`
	DependsOn       []string `yaml:"depends_on"`
	Affects         []string `yaml:"affects"`
	ResourceBudget  *struct {
		RamMB    int     `yaml:"ram_mb"`
		CPUCores float64 `yaml:"cpu_cores"`
	} `yaml:"resource_budget"`
	RequiresAndroid bool   `yaml:"requires_android"`
	Complexity      string `yaml:"complexity"`
	StoryType       string `yaml:"story_type"` // doc | code | mixed (default: code)
}

// ParsedStory carries the frontmatter + the file path it was sourced from.
type ParsedStory struct {
	Frontmatter StoryFrontmatter
	SourceFile  string
	StoryTitle  string // markdown title (### Story X.Y: <title>); fallback for Frontmatter.Title
}

var (
	// Story ID accepts any run of letters/digits/dot/dash/underscore — matches
	// both "4.1" and "1.1.payment-method-management" and slug-style ids.
	storyHeaderRE = regexp.MustCompile(`^###\s+Story\s+([\w.\-]+)\s*[:\-]\s*(.+)$`)
)

// ParseEpicsFile reads an epics.md file and returns every story it contains.
//
// Grammar (per the v6 spec's epics.md layout):
//
//   ### Story <id>: <title>
//
//   ---
//   story_id: "..."
//   depends_on: [...]
//   ...
//   ---
//
//   (free-form body, ignored for planning)
//
// Multiple stories per file. The frontmatter is OPTIONAL — stories without it
// are still emitted (so a freshly-drafted epic doesn't silently disappear),
// with all dependency / affects fields empty.
func ParseEpicsFile(path string) ([]ParsedStory, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open epics %s: %w", path, err)
	}
	defer f.Close()

	var (
		out      []ParsedStory
		current  *ParsedStory
		inYAML   bool
		yamlBuf  strings.Builder
	)

	scan := bufio.NewScanner(f)
	scan.Buffer(make([]byte, 1<<20), 1<<20) // allow long lines
	for scan.Scan() {
		line := scan.Text()

		if m := storyHeaderRE.FindStringSubmatch(line); m != nil {
			// Close any in-progress yaml block from a malformed prior story.
			if current != nil {
				if err := commit(current, &yamlBuf); err != nil {
					return nil, err
				}
				out = append(out, *current)
			}
			current = &ParsedStory{
				SourceFile: path,
				StoryTitle: strings.TrimSpace(m[2]),
				Frontmatter: StoryFrontmatter{StoryID: m[1], Title: strings.TrimSpace(m[2])},
			}
			inYAML = false
			yamlBuf.Reset()
			continue
		}

		if current == nil {
			continue // skip preamble
		}

		switch strings.TrimSpace(line) {
		case "---":
			inYAML = !inYAML
			if !inYAML {
				if err := commit(current, &yamlBuf); err != nil {
					return nil, fmt.Errorf("parse %s story %s: %w", path, current.Frontmatter.StoryID, err)
				}
				yamlBuf.Reset()
			}
			continue
		}

		if inYAML {
			yamlBuf.WriteString(line)
			yamlBuf.WriteString("\n")
		}
	}
	if err := scan.Err(); err != nil {
		return nil, fmt.Errorf("scan %s: %w", path, err)
	}
	if current != nil {
		// EOF — close last open story (even without trailing yaml).
		out = append(out, *current)
	}

	return out, nil
}

// commit merges any buffered yaml into the current story's frontmatter.
func commit(s *ParsedStory, buf *strings.Builder) error {
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
	return nil
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
