package sprint

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/sosalejandro/atlas/packages/codeindex"
	"github.com/sosalejandro/atlas/packages/graph"
	"github.com/sosalejandro/atlas/packages/shared"
)

// EnvIncludeAtlas is the feature-flag env var that gates the atlas-codeindex
// section. When unset or empty the section is omitted entirely. Set to "1",
// "true", or "yes" to enable.
//
// Existing dispatches that already have a bundle on disk are not affected by
// the flag at read-time — only bundle BUILDS consult it.
const EnvIncludeAtlas = "BMAD_CONTEXT_BUNDLE_INCLUDE_ATLAS"

// DefaultCodeContextMaxHops is the dependency-graph walk depth. 2 hops keeps
// the output bounded (typical fanout per symbol is 3-5) while still surfacing
// the immediate collaborators of every affected symbol.
const DefaultCodeContextMaxHops = 2

// DefaultCodeContextMaxRowsPerFile caps the number of indented `file:line`
// rows emitted per affected file. Prevents pathological fan-out (e.g. a
// service constructor called from 200 sites) from blowing up the bundle.
const DefaultCodeContextMaxRowsPerFile = 25

// CodeContextOptions configures BuildCodeContextSection. Zero value is safe:
// it uses default hop count, default per-file caps, and a NopLogger.
type CodeContextOptions struct {
	// MaxHops bounds the BFS depth from each affected symbol. 0 → default.
	MaxHops int

	// MaxRowsPerFile caps emitted rows per affected file. 0 → default.
	MaxRowsPerFile int

	// CacheDir is the directory where serialised Index snapshots are
	// written. Empty → "<repoRoot>/_bmad-output/.codeindex-cache".
	CacheDir string

	// HeadOverride lets tests force a specific cache key. Empty in
	// production — the real HEAD is resolved via `git rev-parse HEAD`.
	HeadOverride string

	// SkipTS forwards through to codeindex.Options.SkipTS. Defaults to true
	// in BuildCodeContextSection (the bundle work is backend-Go-focused);
	// callers that need TS coverage can set this to false explicitly.
	SkipTS bool

	// Logger receives diagnostics. Defaults to NopLogger.
	Logger shared.Logger
}

// indexCache is a process-lifetime cache keyed by `repoRoot|headSHA`. The
// disk cache (CacheDir) is the durable layer; this map is the in-memory
// hot-path that lets a single bundle-build pass run the scanner once.
var (
	indexCacheMu sync.Mutex
	indexCache   = map[string]*codeindex.Index{}
)

// BuildCodeContextSection runs codeindex over repoRoot once (cached by repo
// HEAD commit), walks the dependency graph 1..MaxHops out from every symbol
// whose Position.Path matches an affectedPath, and renders a deterministic
// Markdown block.
//
// Returns an empty string (no error) when:
//   - The feature flag env var is not enabled
//   - affectedPaths is empty
//   - The index has zero symbols matching any affected path
//
// Returns an error only for unrecoverable failures (scanner crash, malformed
// cache file). A missing git/HEAD just disables caching with a warning — the
// index is still computed fresh.
func BuildCodeContextSection(
	ctx context.Context,
	repoRoot string,
	affectedPaths []string,
	opts CodeContextOptions,
) (string, error) {
	if !atlasFlagEnabled() {
		return "", nil
	}
	if repoRoot == "" {
		return "", errors.New("code context: repoRoot required")
	}
	if len(affectedPaths) == 0 {
		return "", nil
	}
	if opts.MaxHops == 0 {
		opts.MaxHops = DefaultCodeContextMaxHops
	}
	if opts.MaxRowsPerFile == 0 {
		opts.MaxRowsPerFile = DefaultCodeContextMaxRowsPerFile
	}
	if opts.Logger == nil {
		opts.Logger = shared.NopLogger{}
	}

	absRoot, err := filepath.Abs(repoRoot)
	if err != nil {
		return "", fmt.Errorf("code context: abs repoRoot: %w", err)
	}

	idx, err := loadOrBuildIndex(ctx, absRoot, opts)
	if err != nil {
		return "", err
	}
	if idx == nil || idx.Graph == nil {
		return "", nil
	}

	// Normalise affectedPaths: dedupe + repo-relative + forward-slash.
	rels := normalisePaths(absRoot, affectedPaths)
	if len(rels) == 0 {
		return "", nil
	}

	// Bucket symbols by their (repo-relative) Position.Path so we can
	// emit per-affected-file sub-trees in a stable order.
	symsByPath := bucketSymbolsByPath(idx.Symbols)

	rendered := renderCodeContext(idx.Graph, symsByPath, rels, opts)
	return rendered, nil
}

// atlasFlagEnabled inspects BMAD_CONTEXT_BUNDLE_INCLUDE_ATLAS. Accepts
// "1", "true", "yes" (case-insensitive). Anything else (including unset)
// disables the section.
func atlasFlagEnabled() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(EnvIncludeAtlas)))
	switch v {
	case "1", "true", "yes":
		return true
	}
	return false
}

func loadOrBuildIndex(ctx context.Context, absRoot string, opts CodeContextOptions) (*codeindex.Index, error) {
	head := opts.HeadOverride
	if head == "" {
		head = resolveGitHead(absRoot)
	}
	cacheKey := absRoot + "|" + head

	if head != "" {
		indexCacheMu.Lock()
		if hit, ok := indexCache[cacheKey]; ok {
			indexCacheMu.Unlock()
			return hit, nil
		}
		indexCacheMu.Unlock()
	}

	cacheDir := opts.CacheDir
	if cacheDir == "" {
		cacheDir = filepath.Join(absRoot, "_bmad-output", ".codeindex-cache")
	}
	cachePath := filepath.Join(cacheDir, cacheKey2filename(head))

	if head != "" {
		if idx, ok := readCachedIndex(cachePath, opts.Logger); ok {
			indexCacheMu.Lock()
			indexCache[cacheKey] = idx
			indexCacheMu.Unlock()
			return idx, nil
		}
	}

	idx, err := codeindex.IndexProject(ctx, absRoot, codeindex.Options{
		SkipTS:    opts.SkipTS,
		HashFiles: false,
		Logger:    opts.Logger,
	})
	if err != nil {
		return nil, fmt.Errorf("code context: codeindex: %w", err)
	}
	if head != "" {
		writeCachedIndex(cachePath, idx, opts.Logger)
		indexCacheMu.Lock()
		indexCache[cacheKey] = idx
		indexCacheMu.Unlock()
	}
	return idx, nil
}

// resolveGitHead runs `git -C <root> rev-parse HEAD`. Returns "" if git is
// missing or the directory isn't a repo — callers treat empty-string as
// "skip caching this run". We do NOT fail the bundle for a missing HEAD.
func resolveGitHead(absRoot string) string {
	cmd := exec.Command("git", "-C", absRoot, "rev-parse", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func cacheKey2filename(head string) string {
	if head == "" {
		return "_nohead.json"
	}
	return head + ".json"
}

func readCachedIndex(path string, logger shared.Logger) (*codeindex.Index, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var idx codeindex.Index
	if err := json.Unmarshal(data, &idx); err != nil {
		logger.Warn(context.Background(), "code context: stale cache",
			"path", path, "err", err.Error())
		return nil, false
	}
	if idx.Graph == nil {
		return nil, false
	}
	return &idx, true
}

func writeCachedIndex(path string, idx *codeindex.Index, logger shared.Logger) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		logger.Warn(context.Background(), "code context: cache mkdir",
			"path", path, "err", err.Error())
		return
	}
	data, err := json.Marshal(idx)
	if err != nil {
		logger.Warn(context.Background(), "code context: cache marshal",
			"err", err.Error())
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		logger.Warn(context.Background(), "code context: cache write",
			"path", tmp, "err", err.Error())
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		logger.Warn(context.Background(), "code context: cache rename",
			"path", path, "err", err.Error())
	}
}

func normalisePaths(absRoot string, in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, p := range in {
		if p == "" {
			continue
		}
		rel := p
		if filepath.IsAbs(p) {
			r, err := filepath.Rel(absRoot, p)
			if err == nil {
				rel = r
			}
		}
		rel = filepath.ToSlash(rel)
		if seen[rel] {
			continue
		}
		seen[rel] = true
		out = append(out, rel)
	}
	sort.Strings(out)
	return out
}

// bucketSymbolsByPath produces a map of repo-relative-path -> sorted symbols
// in that file. Sorting is by Line then ID for stable output.
func bucketSymbolsByPath(syms []shared.Symbol) map[string][]shared.Symbol {
	by := map[string][]shared.Symbol{}
	for _, s := range syms {
		p := filepath.ToSlash(s.Position.Path)
		if p == "" {
			continue
		}
		by[p] = append(by[p], s)
	}
	for k := range by {
		group := by[k]
		sort.Slice(group, func(i, j int) bool {
			if group[i].Position.Line != group[j].Position.Line {
				return group[i].Position.Line < group[j].Position.Line
			}
			return group[i].ID < group[j].ID
		})
		by[k] = group
	}
	return by
}

// renderCodeContext walks the graph and emits the Markdown block. Output is
// deterministic given the same Index + same affectedPaths.
func renderCodeContext(
	g *graph.Graph,
	symsByPath map[string][]shared.Symbol,
	affected []string,
	opts CodeContextOptions,
) string {
	var b strings.Builder
	totalRoots := 0
	perFileRows := map[string]int{}

	type pending struct {
		path string
		rows []string
	}
	var pendings []pending

	for _, relPath := range affected {
		roots := symsByPath[relPath]
		if len(roots) == 0 {
			continue
		}
		totalRoots += len(roots)

		var rows []string
		for _, root := range roots {
			rows = append(rows, formatRow(0, root))
			if perFileRows[relPath]++; perFileRows[relPath] >= opts.MaxRowsPerFile {
				rows = append(rows, fmt.Sprintf("  …(truncated at %d rows for %s)", opts.MaxRowsPerFile, relPath))
				break
			}
			extra := walkNeighbors(g, root.ID, opts.MaxHops, perFileRows, relPath, opts.MaxRowsPerFile)
			rows = append(rows, extra...)
			if perFileRows[relPath] >= opts.MaxRowsPerFile {
				break
			}
		}
		if len(rows) > 0 {
			pendings = append(pendings, pending{path: relPath, rows: rows})
		}
	}

	if totalRoots == 0 {
		return ""
	}

	fmt.Fprintf(&b, "### Code context (from atlas codeindex)\n\n")
	fmt.Fprintf(&b, "Source: atlas/packages/codeindex; graph walk depth = %d hop(s).\n", opts.MaxHops)
	fmt.Fprintf(&b, "Format: `file:line  qualified_name` (indented per hop).\n\n")
	for _, pn := range pendings {
		fmt.Fprintf(&b, "**%s**\n\n```\n", pn.path)
		for _, r := range pn.rows {
			fmt.Fprintln(&b, r)
		}
		fmt.Fprintf(&b, "```\n\n")
	}
	return b.String()
}

// walkNeighbors does a depth-bounded BFS from rootID, returning indented
// `file:line  qualified_name` rows for every visited neighbor (excluding the
// root itself). Cycles are avoided via a visited-set; depth-cap respected.
func walkNeighbors(
	g *graph.Graph,
	rootID shared.SymbolID,
	maxHops int,
	perFileRows map[string]int,
	rootFilePath string,
	maxRowsPerFile int,
) []string {
	if maxHops <= 0 {
		return nil
	}
	type item struct {
		id    shared.SymbolID
		depth int
	}
	visited := map[shared.SymbolID]bool{rootID: true}
	queue := []item{{id: rootID, depth: 0}}
	var rows []string

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur.depth >= maxHops {
			continue
		}
		// Sort callees for determinism.
		callees := g.Callees(cur.id)
		sort.Slice(callees, func(i, j int) bool { return callees[i].ID < callees[j].ID })
		for _, callee := range callees {
			if visited[callee.ID] {
				continue
			}
			visited[callee.ID] = true
			rows = append(rows, formatRow(cur.depth+1, callee.Symbol))
			perFileRows[rootFilePath]++
			if perFileRows[rootFilePath] >= maxRowsPerFile {
				rows = append(rows, fmt.Sprintf("  …(truncated at %d rows for %s)", maxRowsPerFile, rootFilePath))
				return rows
			}
			queue = append(queue, item{id: callee.ID, depth: cur.depth + 1})
		}
	}
	return rows
}

// formatRow renders one "file:line  qualified_name" row with depth-prefixed
// indentation. depth 0 is the root (no indent); depth N gets N "  "
// indentation slots.
func formatRow(depth int, s shared.Symbol) string {
	indent := strings.Repeat("  ", depth)
	path := filepath.ToSlash(s.Position.Path)
	if path == "" {
		// Symbols without a position (e.g. placeholders) still get a row
		// using the qualified name only.
		return fmt.Sprintf("%s%s", indent, s.ID)
	}
	return fmt.Sprintf("%s%s:%d  %s", indent, path, s.Position.Line, s.ID)
}

// ResetIndexCache clears the in-memory cache. Tests use this to force a
// fresh scan between cases. Not exported via CLI — it's a test helper that
// happens to live on the production path so external test packages can call
// it without an `_test.go`-only import boundary.
func ResetIndexCache() {
	indexCacheMu.Lock()
	defer indexCacheMu.Unlock()
	indexCache = map[string]*codeindex.Index{}
}
