package sprint

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// StoryContextSources is the inputs BuildStoryContext reads from. Optional
// paths are skipped when empty; their sections won't appear in the bundle.
type StoryContextSources struct {
	StoryID          string
	EpicsPath        string // required — the lightweight story entry is the bundle's seed
	ArchitecturePath string // optional — FR refs in the entry trigger section extraction here

	// RepoRoot is the project root that the atlas codeindex scanner runs
	// against. Optional; when empty the atlas section is skipped even if
	// the env var is enabled. Typically the same directory the bmad CLI
	// runs from (the bmad invocation `cwd`).
	RepoRoot string

	// CodeContext lets callers override defaults for the atlas section
	// (hop count, cache dir, etc.). Zero value uses defaults.
	CodeContext CodeContextOptions
}

// StoryContextBundle summarises a BuildStoryContext run.
type StoryContextBundle struct {
	StoryID    string   `json:"story_id"`
	OutPath    string   `json:"out_path"`
	Sections   []string `json:"sections"`
	TotalBytes int64    `json:"total_bytes"`
	Warnings   []string `json:"warnings,omitempty"`
	GeneratedAt string  `json:"generated_at"`
}

// ErrStoryNotFoundInEpics is returned by BuildStoryContext when the requested
// StoryID is not present in the supplied epics.md. Exported so the CLI layer
// can map it to exitcode.NotFound (40) instead of the generic exit 1.
// (issue #71)
var ErrStoryNotFoundInEpics = errors.New("story not found in epics")

// IsStoryNotFoundErr reports whether err originated from a missing-story
// lookup in epics.md. Used by `bmad story context-bundle` to choose the
// NOT_FOUND exit code instead of generic user-error.
func IsStoryNotFoundErr(err error) bool {
	return errors.Is(err, ErrStoryNotFoundInEpics)
}

// frRefRE matches Functional-Requirement reference anchors like FR-Arch-7,
// FR-NFR-12, FR-Saga-1, etc. Greedy on the kind so multi-word kinds
// (e.g., FR-Arch-XYZ) still capture.
var frRefRE = regexp.MustCompile(`FR-[A-Za-z]+-\d+`)

// affectedPathRE picks repo-relative paths mentioned in story entries +
// architecture sections. Conservative: anchored to a known top-level
// directory and a known source extension, so prose like "src code" doesn't
// match. The set of top-level dirs matches the canonical monorepo layouts
// (src, apps, packages, internal, cmd, pkg).
//
// Matches inside backticks and bare text. Strips trailing punctuation
// (commas, periods, parens) in postprocessing.
var affectedPathRE = regexp.MustCompile(
	`(?:src|apps|packages|internal|cmd|pkg)/[A-Za-z0-9_./\-]+\.(?:go|ts|tsx|js|jsx)`,
)

// BuildStoryContext extracts a curated per-story context bundle:
//
//   1. The story's lightweight entry from epics.md (extracted by section header)
//   2. For each FR-ref found in the entry: the matching section from
//      architecture.md (header line containing the ref + body until next ##/###)
//
// Output is a single self-contained markdown file. Deterministic across
// runs given identical inputs — supports byte-stable prefix for caching.
//
// Deliberately lean: doesn't try to extract BC audit rows or saga
// participation (those require knowing bc_target / slice, which is only
// reliable post-hydrate; this bundle is meant to land pre-hydrate so the
// hydrator has the smallest useful starting context).
func BuildStoryContext(outPath string, sources StoryContextSources) (*StoryContextBundle, error) {
	if sources.StoryID == "" {
		return nil, errors.New("story context: StoryID required")
	}
	if sources.EpicsPath == "" {
		return nil, errors.New("story context: EpicsPath required")
	}
	if outPath == "" {
		return nil, errors.New("story context: outPath required")
	}

	entry, err := extractStorySection(sources.EpicsPath, sources.StoryID)
	if err != nil {
		return nil, fmt.Errorf("story context: extract entry: %w", err)
	}
	if entry == "" {
		// Sentinel-wrapped so the CLI can exit NOT_FOUND (40) instead of
		// generic UserError (1). Preserves the human-readable message in
		// the Error() string. (issue #71)
		return nil, fmt.Errorf("story context: story %q not found in %s: %w",
			sources.StoryID, sources.EpicsPath, ErrStoryNotFoundInEpics)
	}

	bundle := &StoryContextBundle{
		StoryID:  sources.StoryID,
		OutPath:  outPath,
		Sections: []string{"epics.md story entry"},
	}

	// Render the bundle into outPath.
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return nil, fmt.Errorf("story context: mkdir %s: %w", filepath.Dir(outPath), err)
	}
	out, err := os.Create(outPath)
	if err != nil {
		// Include the absolute path so a confused operator can paste it
		// into ls/stat without guessing whether the relative path was
		// resolved against cwd or somewhere else.
		absOut, _ := filepath.Abs(outPath)
		if absOut == "" {
			absOut = outPath
		}
		return nil, fmt.Errorf("story context: create %s: %w", absOut, err)
	}
	// closeErr captures errors from out.Close() so a flush failure
	// (e.g. disk full mid-write) surfaces as a non-nil return instead of
	// the legacy silent-success-with-empty-file behaviour. (issue #71)
	var closeErr error
	defer func() {
		if cErr := out.Close(); cErr != nil && closeErr == nil {
			closeErr = cErr
		}
	}()

	// Header — small, deterministic.
	header := fmt.Sprintf("# Story %s — context bundle\n\n"+
		"Auto-generated by `bmad story context-bundle %s`. The L3 hydrator "+
		"(or any stage agent) reads this single file as its primary context; "+
		"the standing-corpus sections quoted below are pre-extracted by "+
		"deterministic Go code — no need to grep architecture.md at runtime.\n",
		sources.StoryID, sources.StoryID)
	if _, err := out.WriteString(header); err != nil {
		return nil, fmt.Errorf("story context: write header: %w", err)
	}

	// Section 1: story entry from epics.md.
	if _, err := fmt.Fprintf(out, "\n\n---\n\n## Lightweight story entry\n\nSource: `%s`\n\n%s\n",
		sources.EpicsPath, strings.TrimRight(entry, "\n")); err != nil {
		return nil, fmt.Errorf("story context: write entry: %w", err)
	}

	// Section 2: FR-ref-driven architecture extracts (if architecture path supplied).
	frRefs := uniqueOrdered(frRefRE.FindAllString(entry, -1))
	// archExtractsConcat collects the textual bodies of every emitted arch
	// section so the atlas affected-path scan (Section 3) can pick up file
	// references mentioned inside arch sections in addition to the entry.
	var archExtractsConcat strings.Builder
	if sources.ArchitecturePath != "" && len(frRefs) > 0 {
		extracts, missing, err := extractArchSectionsForFRs(sources.ArchitecturePath, frRefs)
		if err != nil {
			bundle.Warnings = append(bundle.Warnings,
				fmt.Sprintf("architecture scan: %v", err))
		}
		for _, fr := range frRefs {
			body, ok := extracts[fr]
			if !ok {
				continue
			}
			if _, err := fmt.Fprintf(out, "\n\n---\n\n## Architecture section for %s\n\nSource: `%s`\n\n%s\n",
				fr, sources.ArchitecturePath, strings.TrimRight(body, "\n")); err != nil {
				return nil, fmt.Errorf("story context: write arch section %s: %w", fr, err)
			}
			bundle.Sections = append(bundle.Sections, "architecture.md section for "+fr)
			archExtractsConcat.WriteString(body)
			archExtractsConcat.WriteByte('\n')
		}
		for _, fr := range missing {
			bundle.Warnings = append(bundle.Warnings,
				fmt.Sprintf("FR ref %s referenced in entry but no matching architecture section found", fr))
		}
	} else if sources.ArchitecturePath == "" {
		bundle.Warnings = append(bundle.Warnings, "no ArchitecturePath provided — skipped FR-ref extraction")
	}

	// Section 3 (optional): Atlas codeindex code-context. Feature-flagged
	// behind BMAD_CONTEXT_BUNDLE_INCLUDE_ATLAS. Skipped silently when:
	//   - The env var is unset / disabled
	//   - sources.RepoRoot is empty (no project to scan)
	//   - The entry + arch sections mention zero affected file paths
	//   - The codeindex run produces zero symbols matching those paths
	//
	// We deliberately call this BEFORE the footer so the generated-at line
	// stays at the very end of the file — keeps the deterministic-prefix
	// invariant intact for anything upstream that hashes the section
	// headers.
	codeBlock, codeErr := buildCodeContextForBundle(sources, entry, archExtractsConcat.String())
	if codeErr != nil {
		bundle.Warnings = append(bundle.Warnings,
			fmt.Sprintf("atlas code context: %v", codeErr))
	}
	if codeBlock != "" {
		if _, err := fmt.Fprintf(out, "\n\n---\n\n%s", codeBlock); err != nil {
			return nil, fmt.Errorf("story context: write code context: %w", err)
		}
		bundle.Sections = append(bundle.Sections, "atlas codeindex code-context")
	}

	// Footer with generated-at — last bytes; doesn't affect the
	// deterministic-prefix part above.
	bundle.GeneratedAt = time.Now().UTC().Format(time.RFC3339)
	if _, err := fmt.Fprintf(out, "\n\n---\n\n_Generated at %s. Re-run `bmad story context-bundle %s` to refresh._\n",
		bundle.GeneratedAt, sources.StoryID); err != nil {
		return nil, fmt.Errorf("story context: write footer: %w", err)
	}

	// Explicit close before stat — required because some filesystems
	// (and the test fakes) don't expose accurate size until the writer
	// fd is closed. We drive the close here so we can surface its error
	// (issue #71: legacy code deferred-and-discarded Close errors, which
	// hid mid-write flush failures behind a "success" exit). The defer
	// remains as a safety net for early-error paths above.
	if cErr := out.Close(); cErr != nil {
		closeErr = cErr
	}
	if closeErr != nil {
		return nil, fmt.Errorf("story context: close %s: %w", outPath, closeErr)
	}

	info, err := os.Stat(outPath)
	if err != nil {
		return nil, fmt.Errorf("story context: stat %s: %w", outPath, err)
	}
	bundle.TotalBytes = info.Size()
	if bundle.TotalBytes == 0 {
		return nil, fmt.Errorf("story context: wrote zero bytes to %s", outPath)
	}
	return bundle, nil
}

// extractStorySection scans epics.md, finds `### Story <id>:` (or `### Story <id> -`),
// and returns every line up to the next `### ` header (or EOF). Matching is
// strict on the id (no prefix matches) to avoid grabbing 1.10 when looking
// for 1.1.
func extractStorySection(epicsPath, storyID string) (string, error) {
	f, err := os.Open(epicsPath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	// Match `### Story <id>:` or `### Story <id> -` exactly.
	headerRE := regexp.MustCompile(`^###\s+Story\s+` + regexp.QuoteMeta(storyID) + `\s*[:\-]`)

	scan := bufio.NewScanner(f)
	scan.Buffer(make([]byte, 1<<20), 1<<20)

	var (
		out      strings.Builder
		inStory  bool
	)
	for scan.Scan() {
		line := scan.Text()
		if headerRE.MatchString(line) {
			inStory = true
			out.WriteString(line)
			out.WriteString("\n")
			continue
		}
		if inStory {
			// Stop on the next ### header (any story / section).
			if strings.HasPrefix(line, "### ") || strings.HasPrefix(line, "## ") {
				break
			}
			out.WriteString(line)
			out.WriteString("\n")
		}
	}
	if err := scan.Err(); err != nil {
		return "", err
	}
	return out.String(), nil
}

// extractArchSectionsForFRs scans architecture.md for sections whose header
// line mentions any of the FR refs. Returns a map of ref -> section body
// and a list of refs that were searched-for but not found.
//
// "Section" here = the matching header line + everything until the next
// header at the same depth or shallower (e.g., a `### FR-Arch-7 — Foo`
// section ends at the next `###` or `##` or `#`).
func extractArchSectionsForFRs(archPath string, refs []string) (map[string]string, []string, error) {
	f, err := os.Open(archPath)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()

	want := make(map[string]bool, len(refs))
	for _, r := range refs {
		want[r] = true
	}

	results := make(map[string]string, len(refs))

	scan := bufio.NewScanner(f)
	scan.Buffer(make([]byte, 1<<20), 1<<20)

	var (
		currentRef   string
		currentDepth int  // number of leading # chars on the active section's header
		buf          strings.Builder
	)
	flush := func() {
		if currentRef == "" {
			return
		}
		results[currentRef] = buf.String()
		buf.Reset()
		currentRef = ""
		currentDepth = 0
	}

	for scan.Scan() {
		line := scan.Text()
		depth := countLeadingHash(line)
		if depth > 0 {
			// Header line — check if it starts a section we want.
			matched := ""
			for ref := range want {
				if strings.Contains(line, ref) {
					matched = ref
					break
				}
			}
			// If we hit a new header at a depth <= currentDepth, the
			// previous section ends.
			if currentRef != "" && depth <= currentDepth {
				flush()
			}
			if matched != "" && results[matched] == "" {
				currentRef = matched
				currentDepth = depth
				buf.WriteString(line)
				buf.WriteString("\n")
				continue
			}
		}
		if currentRef != "" {
			buf.WriteString(line)
			buf.WriteString("\n")
		}
	}
	flush()
	if err := scan.Err(); err != nil {
		return nil, nil, err
	}

	var missing []string
	for _, r := range refs {
		if _, ok := results[r]; !ok {
			missing = append(missing, r)
		}
	}
	return results, missing, nil
}

// countLeadingHash returns the count of leading # chars (markdown header depth);
// 0 if the line is not a header.
func countLeadingHash(line string) int {
	n := 0
	for n < len(line) && line[n] == '#' {
		n++
	}
	if n == 0 || n >= len(line) || line[n] != ' ' {
		return 0
	}
	return n
}

// uniqueOrdered de-duplicates while preserving first-seen order.
func uniqueOrdered(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// extractAffectedPaths scans story-entry + arch-section text for repo-relative
// source file references. Returns a sorted, de-duplicated slice. Paths that
// fail to exist under repoRoot are dropped silently — the regex captures
// hypothetical paths from prose, so the existence-check is the filter that
// keeps the codeindex feed clean.
//
// When repoRoot is empty no existence check runs (caller is responsible);
// useful for unit tests that want to verify regex behaviour without a real
// project tree.
func extractAffectedPaths(repoRoot string, texts ...string) []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range texts {
		for _, m := range affectedPathRE.FindAllString(t, -1) {
			// Strip trailing punctuation that crept in past the regex
			// (the char class allows `.` and `-` so a path followed by
			// "." can over-match).
			m = strings.TrimRight(m, ".,;:)")
			if seen[m] {
				continue
			}
			if repoRoot != "" {
				if _, err := os.Stat(filepath.Join(repoRoot, m)); err != nil {
					continue
				}
			}
			seen[m] = true
			out = append(out, m)
		}
	}
	// Stable order independent of source-text order so the bundle is
	// byte-reproducible across runs.
	stableSort(out)
	return out
}

func stableSort(in []string) {
	// Tiny helper so the package-internal API stays clean without pulling
	// in sort just for this. Using insertion sort: callers pass <50 paths.
	for i := 1; i < len(in); i++ {
		for j := i; j > 0 && in[j-1] > in[j]; j-- {
			in[j-1], in[j] = in[j], in[j-1]
		}
	}
}

// buildCodeContextForBundle is the BuildStoryContext-internal wrapper around
// BuildCodeContextSection. It owns the path-extraction + repo-root resolution
// logic so storycontext.go's main flow stays linear.
//
// Returns ("", nil) for every "section is correctly omitted" case (flag off,
// no repo root, no affected paths). Returns ("", err) only for unexpected
// failures, which surface as a Warnings entry on the bundle.
func buildCodeContextForBundle(sources StoryContextSources, entry, archConcat string) (string, error) {
	if !atlasFlagEnabled() {
		return "", nil
	}
	if sources.RepoRoot == "" {
		return "", nil
	}
	paths := extractAffectedPaths(sources.RepoRoot, entry, archConcat)
	if len(paths) == 0 {
		return "", nil
	}
	return BuildCodeContextSection(context.Background(), sources.RepoRoot, paths, sources.CodeContext)
}
