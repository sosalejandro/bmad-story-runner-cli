package sprint_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sosalejandro/atlas/packages/codeindex"
	"github.com/sosalejandro/atlas/packages/graph"
	"github.com/sosalejandro/atlas/packages/shared"

	"github.com/sosalejandro/bmad-story-runner-cli/application/sprint"
)

// writeIndexCache primes the on-disk cache for a given (root, head) tuple so
// the tests can drive BuildCodeContextSection with synthetic data without
// running the real atlas scanner.
func writeIndexCache(t *testing.T, cacheDir, head string, idx *codeindex.Index) {
	t.Helper()
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatalf("mkdir cache: %v", err)
	}
	data, err := json.Marshal(idx)
	if err != nil {
		t.Fatalf("marshal idx: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cacheDir, head+".json"), data, 0o644); err != nil {
		t.Fatalf("write cache: %v", err)
	}
}

func buildSyntheticIndex() *codeindex.Index {
	g := graph.New()
	// Three symbols: root in service.go, a callee in repo.go, and a
	// grand-callee in db.go.
	root := shared.Symbol{
		ID:       "service.SaveWithEvents",
		Kind:     shared.KindFunc,
		Position: shared.FilePosition{Path: "src/svc/service.go", Line: 42},
	}
	mid := shared.Symbol{
		ID:       "repo.Save",
		Kind:     shared.KindMethod,
		Position: shared.FilePosition{Path: "src/repo/repo.go", Line: 17},
	}
	leaf := shared.Symbol{
		ID:       "db.Exec",
		Kind:     shared.KindMethod,
		Position: shared.FilePosition{Path: "src/db/db.go", Line: 9},
	}

	g.AddNode(&graph.Node{Symbol: root})
	g.AddNode(&graph.Node{Symbol: mid})
	g.AddNode(&graph.Node{Symbol: leaf})
	g.AddEdge(root.ID, mid.ID)
	g.AddEdge(mid.ID, leaf.ID)

	return &codeindex.Index{
		Root:    "/synthetic",
		Graph:   g,
		Symbols: []shared.Symbol{root, mid, leaf},
		SymbolLangs: map[shared.SymbolID]string{
			root.ID: "go", mid.ID: "go", leaf.ID: "go",
		},
	}
}

func TestBuildCodeContextSection_SyntheticIndex_RendersTree(t *testing.T) {
	t.Setenv(sprint.EnvIncludeAtlas, "1")
	sprint.ResetIndexCache()

	tmp := t.TempDir()
	cacheDir := filepath.Join(tmp, "cache")
	head := "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
	writeIndexCache(t, cacheDir, head, buildSyntheticIndex())

	out, err := sprint.BuildCodeContextSection(
		context.Background(),
		tmp,
		[]string{"src/svc/service.go"},
		sprint.CodeContextOptions{
			CacheDir:     cacheDir,
			HeadOverride: head,
			MaxHops:      2,
			SkipTS:       true,
		},
	)
	if err != nil {
		t.Fatalf("BuildCodeContextSection: %v", err)
	}
	if out == "" {
		t.Fatal("expected non-empty section, got empty")
	}

	// Heading present
	if !strings.Contains(out, "### Code context (from atlas codeindex)") {
		t.Errorf("missing canonical heading\n%s", out)
	}
	// Root row (depth 0, no indent)
	if !strings.Contains(out, "src/svc/service.go:42  service.SaveWithEvents") {
		t.Errorf("missing root row\n%s", out)
	}
	// Depth-1 row (2-space indent) — repo.Save is 1 hop out
	if !strings.Contains(out, "  src/repo/repo.go:17  repo.Save") {
		t.Errorf("missing depth-1 row\n%s", out)
	}
	// Depth-2 row (4-space indent) — db.Exec is 2 hops out
	if !strings.Contains(out, "    src/db/db.go:9  db.Exec") {
		t.Errorf("missing depth-2 row\n%s", out)
	}
}

// Pressure dimension 1 (data-shape): story with no affected files → section
// is omitted entirely (NOT empty heading, NOT placeholder text).
func TestBuildCodeContextSection_NoAffectedPaths_SectionOmitted(t *testing.T) {
	t.Setenv(sprint.EnvIncludeAtlas, "1")
	sprint.ResetIndexCache()

	tmp := t.TempDir()
	out, err := sprint.BuildCodeContextSection(
		context.Background(),
		tmp,
		nil,
		sprint.CodeContextOptions{HeadOverride: "x", CacheDir: filepath.Join(tmp, "c")},
	)
	if err != nil {
		t.Fatalf("BuildCodeContextSection: %v", err)
	}
	if out != "" {
		t.Errorf("expected empty section for no affected paths, got:\n%s", out)
	}
}

// Pressure dimension 2 (boundary): symbols that exist in the index but for a
// DIFFERENT file than what the story mentions → section is omitted.
// Guards against the regression where the scanner accidentally returns
// every symbol in the project because the affected-path filter was bypassed.
func TestBuildCodeContextSection_NoMatchingSymbols_SectionOmitted(t *testing.T) {
	t.Setenv(sprint.EnvIncludeAtlas, "1")
	sprint.ResetIndexCache()

	tmp := t.TempDir()
	cacheDir := filepath.Join(tmp, "cache")
	head := "cafebabe"
	writeIndexCache(t, cacheDir, head, buildSyntheticIndex())

	out, err := sprint.BuildCodeContextSection(
		context.Background(),
		tmp,
		[]string{"src/never/exists.go"},
		sprint.CodeContextOptions{CacheDir: cacheDir, HeadOverride: head},
	)
	if err != nil {
		t.Fatalf("BuildCodeContextSection: %v", err)
	}
	if out != "" {
		t.Errorf("expected empty section for non-matching affected path, got:\n%s", out)
	}
}

// Pressure dimension 3 (cycle / state): circular call graph respects the
// depth cap and doesn't infinite-loop. The synthetic graph is A -> B -> A.
func TestBuildCodeContextSection_Cycle_DepthCapHonored(t *testing.T) {
	t.Setenv(sprint.EnvIncludeAtlas, "1")
	sprint.ResetIndexCache()

	tmp := t.TempDir()
	cacheDir := filepath.Join(tmp, "cache")
	head := "0badf00d"

	g := graph.New()
	a := shared.Symbol{ID: "cyc.A", Kind: shared.KindFunc,
		Position: shared.FilePosition{Path: "src/cyc/a.go", Line: 1}}
	b := shared.Symbol{ID: "cyc.B", Kind: shared.KindFunc,
		Position: shared.FilePosition{Path: "src/cyc/b.go", Line: 2}}
	g.AddNode(&graph.Node{Symbol: a})
	g.AddNode(&graph.Node{Symbol: b})
	g.AddEdge(a.ID, b.ID)
	g.AddEdge(b.ID, a.ID)
	idx := &codeindex.Index{
		Root:        "/synthetic",
		Graph:       g,
		Symbols:     []shared.Symbol{a, b},
		SymbolLangs: map[shared.SymbolID]string{a.ID: "go", b.ID: "go"},
	}
	writeIndexCache(t, cacheDir, head, idx)

	out, err := sprint.BuildCodeContextSection(
		context.Background(),
		tmp,
		[]string{"src/cyc/a.go"},
		sprint.CodeContextOptions{CacheDir: cacheDir, HeadOverride: head, MaxHops: 5},
	)
	if err != nil {
		t.Fatalf("BuildCodeContextSection: %v", err)
	}
	// Both nodes show up exactly once each — A at depth 0, B at depth 1.
	// The B -> A back-edge is short-circuited by the visited set.
	if strings.Count(out, "cyc.A") != 1 {
		t.Errorf("expected cyc.A to appear exactly once, got %d occurrences\n%s",
			strings.Count(out, "cyc.A"), out)
	}
	if strings.Count(out, "cyc.B") != 1 {
		t.Errorf("expected cyc.B to appear exactly once, got %d occurrences\n%s",
			strings.Count(out, "cyc.B"), out)
	}
}

// Pressure dimension 4 (authorization / config): env-var disabled → no
// section even when affected paths + index would otherwise produce output.
func TestBuildCodeContextSection_FeatureFlagDisabled_SectionOmitted(t *testing.T) {
	// Explicitly unset (Setenv with empty string + Unsetenv aren't
	// equivalent under t.Setenv on some platforms — using "" forces the
	// flag-off branch deterministically).
	t.Setenv(sprint.EnvIncludeAtlas, "")
	sprint.ResetIndexCache()

	tmp := t.TempDir()
	cacheDir := filepath.Join(tmp, "cache")
	head := "feed1234"
	writeIndexCache(t, cacheDir, head, buildSyntheticIndex())

	out, err := sprint.BuildCodeContextSection(
		context.Background(),
		tmp,
		[]string{"src/svc/service.go"},
		sprint.CodeContextOptions{CacheDir: cacheDir, HeadOverride: head},
	)
	if err != nil {
		t.Fatalf("BuildCodeContextSection: %v", err)
	}
	if out != "" {
		t.Errorf("expected empty section when feature flag off, got:\n%s", out)
	}
}

// Determinism: same head + same affected paths → byte-identical output.
func TestBuildCodeContextSection_Deterministic(t *testing.T) {
	t.Setenv(sprint.EnvIncludeAtlas, "1")
	sprint.ResetIndexCache()

	tmp := t.TempDir()
	cacheDir := filepath.Join(tmp, "cache")
	head := "abc123"
	writeIndexCache(t, cacheDir, head, buildSyntheticIndex())

	run := func() string {
		sprint.ResetIndexCache()
		out, err := sprint.BuildCodeContextSection(
			context.Background(),
			tmp,
			[]string{"src/svc/service.go", "src/repo/repo.go"},
			sprint.CodeContextOptions{CacheDir: cacheDir, HeadOverride: head, MaxHops: 2},
		)
		if err != nil {
			t.Fatalf("BuildCodeContextSection: %v", err)
		}
		return out
	}
	a := run()
	b := run()
	if a != b {
		t.Errorf("non-deterministic output:\n--- run 1 ---\n%s\n--- run 2 ---\n%s", a, b)
	}
	if a == "" {
		t.Fatal("expected non-empty output on first run")
	}
}

// BuildStoryContext: integration of the atlas section with the rest of the
// bundle. Story 1.2 entry mentions src/.../save_with_events.go which is
// pre-cached in our synthetic index.
func TestBuildStoryContext_IncludesAtlasSection_WhenFlagOn(t *testing.T) {
	t.Setenv(sprint.EnvIncludeAtlas, "1")
	sprint.ResetIndexCache()

	tmp := t.TempDir()
	// Simulate a repo root with the affected file present on disk.
	storyFilePath := "src/svc/service.go"
	if err := os.MkdirAll(filepath.Join(tmp, "src/svc"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmp, storyFilePath), []byte("package svc\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	// Cache a synthetic index keyed by a forced HEAD.
	cacheDir := filepath.Join(tmp, "_bmad-output", ".codeindex-cache")
	head := "test1234"
	writeIndexCache(t, cacheDir, head, buildSyntheticIndex())

	epicsPath := filepath.Join(tmp, "epics.md")
	if err := os.WriteFile(epicsPath, []byte(`# Sample
## Epic 1: Foundation

### Story 1.2: Save With Events

- Touches `+"`"+`src/svc/service.go`+"`"+` to add the helper.
- **Refs:** FR-Arch-2
`), 0o644); err != nil {
		t.Fatalf("write epics: %v", err)
	}

	res, err := sprint.BuildStoryContext(filepath.Join(tmp, "out.md"), sprint.StoryContextSources{
		StoryID:   "1.2",
		EpicsPath: epicsPath,
		RepoRoot:  tmp,
		CodeContext: sprint.CodeContextOptions{
			CacheDir:     cacheDir,
			HeadOverride: head,
			MaxHops:      2,
		},
	})
	if err != nil {
		t.Fatalf("BuildStoryContext: %v", err)
	}
	body, _ := os.ReadFile(res.OutPath)
	got := string(body)
	if !strings.Contains(got, "### Code context (from atlas codeindex)") {
		t.Errorf("bundle missing atlas section\n%s", got)
	}
	if !strings.Contains(got, "src/svc/service.go:42  service.SaveWithEvents") {
		t.Errorf("bundle missing affected-file row\n%s", got)
	}
	if !containsSection(res.Sections, "atlas codeindex code-context") {
		t.Errorf("bundle.Sections missing the atlas entry: %v", res.Sections)
	}
}

func TestBuildStoryContext_OmitsAtlasSection_WhenFlagOff(t *testing.T) {
	t.Setenv(sprint.EnvIncludeAtlas, "")
	sprint.ResetIndexCache()

	tmp := t.TempDir()
	epicsPath := filepath.Join(tmp, "epics.md")
	_ = os.WriteFile(epicsPath, []byte(`### Story 1.2: X

- Touches `+"`"+`src/svc/service.go`+"`"+`
- **Refs:** FR-Arch-2
`), 0o644)

	res, err := sprint.BuildStoryContext(filepath.Join(tmp, "out.md"), sprint.StoryContextSources{
		StoryID:   "1.2",
		EpicsPath: epicsPath,
		RepoRoot:  tmp,
	})
	if err != nil {
		t.Fatalf("BuildStoryContext: %v", err)
	}
	body, _ := os.ReadFile(res.OutPath)
	got := string(body)
	if strings.Contains(got, "### Code context (from atlas codeindex)") {
		t.Errorf("bundle contains atlas section despite flag off\n%s", got)
	}
}

func containsSection(sections []string, want string) bool {
	for _, s := range sections {
		if s == want {
			return true
		}
	}
	return false
}

// Integration: scan the live nutrition-v2-go checkout, build a bundle for
// Story 1.2 (which exists in docs/architecture/eda-cutover/epics.md). Story
// 1.2 references files that the story itself CREATES (save_with_events.go,
// load_and_mutate.go), so they don't exist on disk yet — the affected-path
// existence-check correctly filters them out and the atlas section is
// expected to be absent. This is the "story whose affected files don't
// exist yet" case from the task spec: the section must be OMITTED, not
// emitted-empty. Skipped cleanly when the checkout is absent so this test
// is safe in CI sandboxes.
func TestBuildStoryContext_NutritionV2Go_Story12_NoExistingFiles(t *testing.T) {
	const (
		repoRoot  = "/home/alejandrososa/Documents/startup-projects/nutrition-v2-go"
		epicsPath = "docs/architecture/eda-cutover/epics.md"
		archPath  = "docs/architecture/eda-cutover/architecture.md"
	)
	if _, err := os.Stat(filepath.Join(repoRoot, epicsPath)); err != nil {
		t.Skipf("nutrition-v2-go checkout not present at %s: %v", repoRoot, err)
	}

	t.Setenv(sprint.EnvIncludeAtlas, "1")
	sprint.ResetIndexCache()

	tmp := t.TempDir()
	res, err := sprint.BuildStoryContext(filepath.Join(tmp, "1.2.md"), sprint.StoryContextSources{
		StoryID:          "1.2",
		EpicsPath:        filepath.Join(repoRoot, epicsPath),
		ArchitecturePath: filepath.Join(repoRoot, archPath),
		RepoRoot:         repoRoot,
		CodeContext: sprint.CodeContextOptions{
			CacheDir: filepath.Join(tmp, "cache"),
			SkipTS:   true,
		},
	})
	if err != nil {
		t.Fatalf("BuildStoryContext: %v", err)
	}
	body, _ := os.ReadFile(res.OutPath)
	got := string(body)
	if !strings.Contains(got, "Story 1.2") {
		t.Fatal("bundle is missing the 1.2 entry")
	}
	if strings.Contains(got, "### Code context (from atlas codeindex)") {
		t.Errorf("Story 1.2 references only not-yet-existing files; atlas section should be omitted but is present\n%s",
			truncate(got, 2000))
	}
}

// Integration: scan the live nutrition-v2-go checkout, build a bundle for
// Story 4.9 (ISP audit on identity interfaces) which explicitly references
// `src/contexts/identity/application/services/ports.go` — a file that
// exists on disk. The atlas section MUST appear and have at least one
// `file:line  qualified_name` row.
func TestBuildStoryContext_NutritionV2Go_Story49_HasCodeContext(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping nutrition-v2-go scan in -short mode (full repo scan ~25s)")
	}
	const (
		repoRoot        = "/home/alejandrososa/Documents/startup-projects/nutrition-v2-go"
		epicsPath       = "docs/architecture/eda-cutover/epics.md"
		archPath        = "docs/architecture/eda-cutover/architecture.md"
		expectedFileRef = "src/contexts/identity/application/services/ports.go"
	)
	if _, err := os.Stat(filepath.Join(repoRoot, expectedFileRef)); err != nil {
		t.Skipf("nutrition-v2-go checkout or expected file missing: %v", err)
	}

	t.Setenv(sprint.EnvIncludeAtlas, "1")
	sprint.ResetIndexCache()

	tmp := t.TempDir()
	res, err := sprint.BuildStoryContext(filepath.Join(tmp, "4.9.md"), sprint.StoryContextSources{
		StoryID:          "4.9",
		EpicsPath:        filepath.Join(repoRoot, epicsPath),
		ArchitecturePath: filepath.Join(repoRoot, archPath),
		RepoRoot:         repoRoot,
		CodeContext: sprint.CodeContextOptions{
			CacheDir: filepath.Join(tmp, "cache"),
			SkipTS:   true,
		},
	})
	if err != nil {
		t.Fatalf("BuildStoryContext: %v", err)
	}
	body, _ := os.ReadFile(res.OutPath)
	got := string(body)
	if !strings.Contains(got, "Story 4.9") {
		t.Fatal("bundle is missing the 4.9 entry")
	}
	if !strings.Contains(got, "### Code context (from atlas codeindex)") {
		t.Errorf("Story 4.9 mentions an existing file; atlas section should appear but is missing\n%s",
			truncate(got, 4000))
	}
	if !strings.Contains(got, expectedFileRef) {
		t.Errorf("expected the atlas section to mention %s, did not find it.\n%s",
			expectedFileRef, truncate(got, 4000))
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "...(truncated)"
}
