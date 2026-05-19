package inferdeps

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
)

// PatchResult is the structured JSON-mode envelope payload for the
// command. Keeping it as a named struct (rather than map[string]any) gives
// downstream consumers a stable shape they can decode into.
type PatchResult struct {
	EpicsFile         string       `json:"epics_file"`
	TotalStories      int          `json:"total_stories"`
	StoriesWithCues   int          `json:"stories_with_cues"`
	StoriesWithNew    int          `json:"stories_with_new_deps"`
	Suggestions       []Suggestion `json:"suggestions"`
	Applied           bool         `json:"applied"`
	StoriesPatched    int          `json:"stories_patched"`
	BackupPath        string       `json:"backup_path,omitempty"`
	ScopeEpic         string       `json:"scope_epic,omitempty"`
	IntraEpicFallback bool         `json:"intra_epic_fallback"`
	Agreement         *Agreement   `json:"agreement,omitempty"`
}

// Agreement is the tool-vs-manual alignment report — for any story that
// has frontmatter depends_on AND the tool produced suggestions, we count
// matches vs misses. The PR description / nutrition smoke uses this.
type Agreement struct {
	StoriesScored         int     `json:"stories_scored"`
	InferredDepCount      int     `json:"inferred_dep_count"`
	MatchedDepCount       int     `json:"matched_dep_count"`
	AgreementRatePercent  float64 `json:"agreement_rate_percent"`
	FrontmatterDepsTotal  int     `json:"frontmatter_deps_total"`
}

// EmitSummary writes a human-readable summary of suggestions to w. It's
// the text-mode counterpart to the JSON envelope.
func EmitSummary(w io.Writer, result *PatchResult) error {
	fmt.Fprintf(w, "epics file: %s\n", result.EpicsFile)
	if result.ScopeEpic != "" {
		fmt.Fprintf(w, "scope: epic %s\n", result.ScopeEpic)
	}
	fmt.Fprintf(w, "stories: %d total | %d with inferred cues | %d with NEW deps\n",
		result.TotalStories, result.StoriesWithCues, result.StoriesWithNew)
	if result.Agreement != nil && result.Agreement.StoriesScored > 0 {
		fmt.Fprintf(w, "agreement vs manual: %.1f%% (%d matched / %d inferred across %d scored stories)\n",
			result.Agreement.AgreementRatePercent,
			result.Agreement.MatchedDepCount,
			result.Agreement.InferredDepCount,
			result.Agreement.StoriesScored)
	}
	fmt.Fprintln(w)

	for _, sug := range result.Suggestions {
		newDeps := sug.NewDeps()
		if len(newDeps) == 0 {
			continue
		}
		fmt.Fprintf(w, "# Suggested for Story %s — %s\n", sug.StoryID, sug.Title)
		// Render merged list as the actual yaml fragment the operator
		// pastes in. New ids are flagged inline so reviewers see what's
		// being added.
		merged := sug.MergedDeps()
		fmt.Fprintf(w, "depends_on: [%s]\n", joinYAMLStrings(merged))
		for _, d := range newDeps {
			fmt.Fprintf(w, "  # + %s  (%s, via %s — %q)\n", d.DepID, d.Confidence, d.Source, d.Cue)
		}
		fmt.Fprintln(w)
	}
	if result.Applied {
		fmt.Fprintf(w, "applied: rewrote %d stories in %s\n", result.StoriesPatched, result.EpicsFile)
		if result.BackupPath != "" {
			fmt.Fprintf(w, "backup: %s\n", result.BackupPath)
		}
	}
	return nil
}

func joinYAMLStrings(ids []string) string {
	if len(ids) == 0 {
		return ""
	}
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = fmt.Sprintf("%q", id)
	}
	return strings.Join(parts, ", ")
}

// ComputeAgreement scores tool suggestions against existing frontmatter
// depends_on entries. Only stories that have BOTH frontmatter deps AND at
// least one inferred dep contribute to the score.
func ComputeAgreement(sugs []Suggestion) Agreement {
	agg := Agreement{}
	for _, s := range sugs {
		if len(s.FrontmatterDependsOn) == 0 {
			continue
		}
		agg.FrontmatterDepsTotal += len(s.FrontmatterDependsOn)
		if len(s.InferredDeps) == 0 {
			continue
		}
		agg.StoriesScored++
		agg.InferredDepCount += len(s.InferredDeps)
		agg.MatchedDepCount += len(s.MatchedDeps())
	}
	if agg.InferredDepCount > 0 {
		agg.AgreementRatePercent = 100.0 *
			float64(agg.MatchedDepCount) / float64(agg.InferredDepCount)
	}
	return agg
}

// FilterByEpic keeps only suggestions whose StoryID belongs to the given
// epic id. Empty epicID is a no-op (returns the input unchanged).
func FilterByEpic(sugs []Suggestion, epicID string) []Suggestion {
	if epicID == "" {
		return sugs
	}
	var out []Suggestion
	for _, s := range sugs {
		if s.EpicID == epicID {
			out = append(out, s)
		}
	}
	return out
}

// ApplyPatches rewrites the epics.md file in place: for each story that
// has frontmatter AND new inferred deps, the existing `depends_on:` line
// (or the block-list form) is replaced with the merged list. Stories
// without frontmatter are skipped (warning surfaced through the returned
// skipped-list). The result is byte-identical to the input EXCEPT for the
// rewritten depends_on lines.
//
// When backup is true a `.bak` copy of the original is written next to
// the source.
func ApplyPatches(epicsPath string, sugs []Suggestion, backup bool) (patched int, backupPath string, skipped []string, err error) {
	// Index suggestions by story id for quick lookup while walking.
	byID := map[string]Suggestion{}
	for _, s := range sugs {
		if len(s.NewDeps()) == 0 {
			continue
		}
		if !s.HasFrontmatter {
			skipped = append(skipped, s.StoryID)
			continue
		}
		byID[s.StoryID] = s
	}
	if len(byID) == 0 {
		return 0, "", skipped, nil
	}

	src, err := os.ReadFile(epicsPath)
	if err != nil {
		return 0, "", skipped, fmt.Errorf("read %s: %w", epicsPath, err)
	}
	if backup {
		backupPath = epicsPath + ".bak"
		if werr := os.WriteFile(backupPath, src, 0o644); werr != nil {
			return 0, "", skipped, fmt.Errorf("write backup %s: %w", backupPath, werr)
		}
	}

	out, n := rewriteDependsOn(string(src), byID)

	if werr := os.WriteFile(epicsPath, []byte(out), 0o644); werr != nil {
		return 0, backupPath, skipped, fmt.Errorf("write %s: %w", epicsPath, werr)
	}
	return n, backupPath, skipped, nil
}

var (
	// Matches BOTH inline ("depends_on: [...]") and the start of a block
	// form ("depends_on:" with no inline list).
	depsLineRE       = regexp.MustCompile(`^(\s*depends_on\s*:)\s*(.*)$`)
	storyHeaderApply = regexp.MustCompile(`^###\s+Story\s+([\w.\-]+)\s*[:\-]\s*(.+)$`)
)

// rewriteDependsOn does the in-place text replacement. We walk lines and
// track:
//   currentStoryID: which story's frontmatter (if any) we're inside
//   inYAML:         whether we're between `---` delimiters
//   inBlockList:    whether we're consuming a multi-line depends_on block
//                   that we need to skip (its replacement has already been
//                   emitted before this block).
func rewriteDependsOn(src string, byID map[string]Suggestion) (string, int) {
	var (
		buf            strings.Builder
		currentStory   string
		inYAML         bool
		inBlockSkip    bool
		patched        = 0
		hasDependsLine = map[string]bool{}
	)
	scan := bufio.NewScanner(strings.NewReader(src))
	scan.Buffer(make([]byte, 1<<20), 1<<20)
	for scan.Scan() {
		line := scan.Text()

		if m := storyHeaderApply.FindStringSubmatch(line); m != nil {
			currentStory = m[1]
			inYAML = false
			inBlockSkip = false
			buf.WriteString(line)
			buf.WriteByte('\n')
			continue
		}

		if strings.TrimSpace(line) == "---" && currentStory != "" {
			// Frontmatter open/close. On close, if this story was supposed
			// to get a depends_on rewrite but had none, the line never
			// fired — handled below.
			if inYAML && !hasDependsLine[currentStory] {
				if sug, ok := byID[currentStory]; ok {
					buf.WriteString(renderDependsOnLine(sug))
				}
			}
			inYAML = !inYAML
			inBlockSkip = false
			buf.WriteString(line)
			buf.WriteByte('\n')
			continue
		}

		if inYAML {
			// Continue skipping a block-list we already replaced.
			if inBlockSkip {
				trim := strings.TrimSpace(line)
				if strings.HasPrefix(trim, "- ") || trim == "" {
					// Treat empty line as terminator for safety — many epic
					// authors use blank lines after a list. Continue skipping
					// only true list-item lines.
					if strings.HasPrefix(trim, "- ") {
						continue
					}
					inBlockSkip = false
					buf.WriteString(line)
					buf.WriteByte('\n')
					continue
				}
				inBlockSkip = false
				// Fallthrough to write the non-list line.
			}

			if m := depsLineRE.FindStringSubmatch(line); m != nil {
				hasDependsLine[currentStory] = true
				if sug, ok := byID[currentStory]; ok {
					buf.WriteString(renderDependsOnLine(sug))
					patched++
					inline := strings.TrimSpace(m[2])
					if inline == "" {
						// block-list form — skip subsequent `- ` lines
						inBlockSkip = true
					}
					continue
				}
				// No suggestion for this story; keep the line as-is.
				buf.WriteString(line)
				buf.WriteByte('\n')
				continue
			}
		}

		buf.WriteString(line)
		buf.WriteByte('\n')
	}
	return buf.String(), patched
}

func renderDependsOnLine(s Suggestion) string {
	return fmt.Sprintf("depends_on: [%s]\n", joinYAMLStrings(s.MergedDeps()))
}
