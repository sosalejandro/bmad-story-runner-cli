package inferdeps

import (
	"regexp"
	"sort"
	"strings"
)

// Confidence labels per inferred dep. HIGH = explicit story id; MEDIUM =
// slice/epic name resolved through the lookup table; LOW = intra-epic
// fallback (Story X.Y → X.(Y-1) when no other cues fired).
type Confidence string

const (
	ConfidenceHigh   Confidence = "HIGH"
	ConfidenceMedium Confidence = "MEDIUM"
	ConfidenceLow    Confidence = "LOW"
)

// Inferred is one suggested dependency for one story. The (StoryID, DepID)
// pair is unique within a single resolver run.
type Inferred struct {
	StoryID    string     `json:"story_id"`
	DepID      string     `json:"dep_id"`
	Confidence Confidence `json:"confidence"`
	Cue        string     `json:"cue"`     // the raw substring that triggered the inference
	Source     string     `json:"source"`  // "given" | "intra-epic" | "refs"
}

// Suggestion is the per-story aggregate. Deps are sorted (story-id natural
// order), deduplicated, and never contain self-references.
type Suggestion struct {
	StoryID              string     `json:"story_id"`
	Title                string     `json:"title"`
	EpicID               string     `json:"epic_id"`
	InferredDeps         []Inferred `json:"inferred_deps"`
	FrontmatterDependsOn []string   `json:"frontmatter_depends_on"`
	HasFrontmatter       bool       `json:"has_frontmatter"`
}

// MergedDeps returns the union of frontmatter-declared + inferred dep ids,
// deduped, sorted in natural story-id order. Useful for `--apply` patches
// that need a complete depends_on list.
func (s Suggestion) MergedDeps() []string {
	seen := map[string]bool{}
	var out []string
	for _, d := range s.FrontmatterDependsOn {
		if !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	for _, d := range s.InferredDeps {
		if !seen[d.DepID] {
			seen[d.DepID] = true
			out = append(out, d.DepID)
		}
	}
	sort.Slice(out, func(i, j int) bool { return naturalLess(out[i], out[j]) })
	return out
}

// NewDeps returns the inferred deps NOT already present in the frontmatter.
// This is what an operator wants when reviewing dry-run output — they only
// need to consider novel suggestions.
func (s Suggestion) NewDeps() []Inferred {
	have := map[string]bool{}
	for _, d := range s.FrontmatterDependsOn {
		have[d] = true
	}
	var out []Inferred
	for _, d := range s.InferredDeps {
		if !have[d.DepID] {
			out = append(out, d)
		}
	}
	return out
}

// MatchedDeps returns the inferred deps that were already in the frontmatter.
// Used by the agreement-rate harness to compute tool-vs-manual alignment.
func (s Suggestion) MatchedDeps() []Inferred {
	have := map[string]bool{}
	for _, d := range s.FrontmatterDependsOn {
		have[d] = true
	}
	var out []Inferred
	for _, d := range s.InferredDeps {
		if have[d.DepID] {
			out = append(out, d)
		}
	}
	return out
}

// Resolve turns parsed prose into Suggestion entries. The resolver is
// deterministic: same input → same output, ordering included.
//
//	enableIntraEpic: when true, stories with no other inferred deps and
//	  StoryID == "X.Y" with Y > 1 get a LOW-confidence X.(Y-1) suggestion.
//	  This is the "Story X.2 builds on X.1" heuristic — useful when prose
//	  is sparse but cuts noise when prose is rich.
func Resolve(parsed *ParsedEpicsProse, enableIntraEpic bool) []Suggestion {
	if parsed == nil {
		return nil
	}

	// Build helper indexes:
	//   sliceToEpic: "0a" → "1", "13" → "16", ...
	//   lastStoryOfEpic: "1" → "1.4", "4" → "4.12", ... (the highest
	//     story id within each epic in DOCUMENT ORDER — i.e. the last one
	//     that appears in the source file, which matches the intuitive
	//     "epic 4 done" semantics).
	//   storyExists: set of all story ids actually defined, so we can
	//     filter inferences that point at non-existent ids.
	sliceToEpic := map[string]string{}
	for _, e := range parsed.Epics {
		if e.Slice != "" {
			sliceToEpic[e.Slice] = e.EpicID
		}
	}
	lastStoryOfEpic := map[string]string{}
	storyExists := map[string]bool{}
	for _, s := range parsed.Stories {
		storyExists[s.StoryID] = true
		lastStoryOfEpic[s.EpicID] = s.StoryID
	}

	var out []Suggestion
	for _, story := range parsed.Stories {
		sug := Suggestion{
			StoryID:              story.StoryID,
			Title:                story.Title,
			EpicID:               story.EpicID,
			FrontmatterDependsOn: append([]string(nil), story.FrontmatterDependsOn...),
			HasFrontmatter:       story.HasFrontmatter,
		}

		// Track de-dup within a single story's inferences.
		seen := map[string]bool{}
		add := func(depID string, conf Confidence, cue, source string) {
			if depID == "" || depID == story.StoryID {
				return
			}
			if !storyExists[depID] {
				return
			}
			if seen[depID] {
				return
			}
			seen[depID] = true
			sug.InferredDeps = append(sug.InferredDeps, Inferred{
				StoryID:    story.StoryID,
				DepID:      depID,
				Confidence: conf,
				Cue:        cue,
				Source:     source,
			})
		}

		for _, g := range story.GivenLines {
			extractCues(g, sliceToEpic, lastStoryOfEpic, story.StoryID, "given", add)
		}
		for _, r := range story.RefsLines {
			extractCues(r, sliceToEpic, lastStoryOfEpic, story.StoryID, "refs", add)
		}

		if enableIntraEpic && len(sug.InferredDeps) == 0 {
			if pred := intraEpicPredecessor(story.StoryID); pred != "" && storyExists[pred] {
				add(pred, ConfidenceLow, "intra-epic fallback (Story X.Y → X.(Y-1))", "intra-epic")
			}
		}

		// Stabilise order: confidence (HIGH > MEDIUM > LOW) then natural
		// story-id order. Stable output keeps tests + diffs predictable.
		sort.SliceStable(sug.InferredDeps, func(i, j int) bool {
			ci, cj := confRank(sug.InferredDeps[i].Confidence), confRank(sug.InferredDeps[j].Confidence)
			if ci != cj {
				return ci < cj
			}
			return naturalLess(sug.InferredDeps[i].DepID, sug.InferredDeps[j].DepID)
		})
		out = append(out, sug)
	}
	return out
}

// Cue patterns. We compile once; the resolver runs these per Given/Refs line.
var (
	// Explicit story id reference. Matches:
	//   Story 4.1
	//   Story 1.1.foo-bar
	//   story 10.3
	storyMentionRE = regexp.MustCompile(`(?i)Story\s+([0-9]+\.[0-9A-Za-z._\-]+)`)

	// "Slice 0a", "Slice 13", "Slices 1-5" (we only fire on the first id in a range)
	sliceMentionRE = regexp.MustCompile(`(?i)Slice[s]?\s+([0-9]+[a-z]?)(?:\s*[-–]\s*([0-9]+[a-z]?))?`)

	// "Epic 4", "Epic 13"
	epicMentionRE = regexp.MustCompile(`(?i)Epic\s+(\d+)`)
)

// extractCues runs every recogniser against one prose line.
func extractCues(line string, sliceToEpic, lastStoryOfEpic map[string]string, selfID, source string, add func(string, Confidence, string, string)) {
	// 1) explicit story id — HIGH
	for _, m := range storyMentionRE.FindAllStringSubmatch(line, -1) {
		add(m[1], ConfidenceHigh, condense(line), source)
	}

	// 2) slice mentions — MEDIUM (resolves to last-story-of-epic)
	for _, m := range sliceMentionRE.FindAllStringSubmatch(line, -1) {
		// Range form ("Slices 1-5"): map each id in the range that exists.
		ids := []string{m[1]}
		if m[2] != "" {
			ids = append(ids, expandSliceRange(m[1], m[2])...)
		}
		for _, sid := range ids {
			ep, ok := sliceToEpic[sid]
			if !ok {
				continue
			}
			last := lastStoryOfEpic[ep]
			if last == "" || sameEpic(selfID, ep) {
				// Skip when the slice references the same epic — we want
				// cross-epic deps from slice mentions, not self-loops.
				continue
			}
			add(last, ConfidenceMedium, condense(line), source)
		}
	}

	// 3) epic mentions — MEDIUM
	for _, m := range epicMentionRE.FindAllStringSubmatch(line, -1) {
		ep := m[1]
		if sameEpic(selfID, ep) {
			continue
		}
		last := lastStoryOfEpic[ep]
		if last == "" {
			continue
		}
		add(last, ConfidenceMedium, condense(line), source)
	}
}

// expandSliceRange returns the intermediate slice ids inside an "A-B"
// pattern, exclusive of the endpoints (the caller already adds those).
// Only numeric ids participate; "0a-0c" is not expanded (returns nothing
// rather than guess).
func expandSliceRange(a, b string) []string {
	if !isAllDigits(a) || !isAllDigits(b) {
		return nil
	}
	ai, bi := atoi(a), atoi(b)
	if ai >= bi {
		return nil
	}
	var out []string
	for i := ai + 1; i < bi; i++ {
		out = append(out, itoa(i))
	}
	out = append(out, b) // include endpoint
	return out
}

// sameEpic checks if a story id and an epic id share the same epic prefix
// (e.g. "4.2" and "4" → true).
func sameEpic(storyID, epicID string) bool {
	if i := strings.IndexByte(storyID, '.'); i >= 0 {
		return storyID[:i] == epicID
	}
	return storyID == epicID
}

// intraEpicPredecessor returns "X.Y-1" when storyID is "X.Y" with integer
// Y > 1. Returns "" otherwise (epic-level stories, sub-story ids, etc).
func intraEpicPredecessor(storyID string) string {
	parts := strings.SplitN(storyID, ".", 2)
	if len(parts) != 2 {
		return ""
	}
	suffix := parts[1]
	// Only handle pure-numeric suffixes — "1.payment-method" doesn't get
	// a fallback predecessor.
	if !isAllDigits(suffix) {
		return ""
	}
	n := atoi(suffix)
	if n <= 1 {
		return ""
	}
	return parts[0] + "." + itoa(n-1)
}

func confRank(c Confidence) int {
	switch c {
	case ConfidenceHigh:
		return 0
	case ConfidenceMedium:
		return 1
	default:
		return 2
	}
}

// condense trims a cue down to a fixed length for output readability.
func condense(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 96 {
		return s[:93] + "..."
	}
	return s
}

// naturalLess compares two story ids like "4.10" and "4.2" as humans
// expect (4.2 < 4.10). Pure lexicographic compare flips that.
func naturalLess(a, b string) bool {
	pa := strings.Split(a, ".")
	pb := strings.Split(b, ".")
	for i := 0; i < len(pa) && i < len(pb); i++ {
		ai := atoi(pa[i])
		bi := atoi(pb[i])
		if isAllDigits(pa[i]) && isAllDigits(pb[i]) {
			if ai != bi {
				return ai < bi
			}
			continue
		}
		if pa[i] != pb[i] {
			return pa[i] < pb[i]
		}
	}
	return len(pa) < len(pb)
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func atoi(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return n
		}
		n = n*10 + int(r-'0')
	}
	return n
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}
