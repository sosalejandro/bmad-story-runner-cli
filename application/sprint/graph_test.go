package sprint_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/sosalejandro/bmad-story-runner-cli/application/sprint"
	"github.com/sosalejandro/bmad-story-runner-cli/domain/state"
	"github.com/sosalejandro/bmad-story-runner-cli/infrastructure/state/sqlite"
)

// ----------------------------------------------------------------------------
// 4-epic synthetic DAG fixture
//
//   1.1 ─┐
//   1.2 ─┴─► 2.1 ─► 3.1
//           2.2 ─► 3.2 ─► 4.1
//                          4.2 (depends on 3.2 + 4.1)
//
// Stories carry mixed statuses so every renderer branch is exercised in
// one shot — green/yellow/red/grey nodes all appear.
// ----------------------------------------------------------------------------

type fixtureStory struct {
	id, title string
	status    state.Status
	deps      []string
}

var graphFixture = []fixtureStory{
	{id: "1.1", title: "epics scaffold parser", status: state.StatusComplete},
	{id: "1.2", title: "frontmatter coverage", status: state.StatusComplete},
	{id: "2.1", title: "topo-sort batcher", status: state.StatusInProgress, deps: []string{"1.1", "1.2"}},
	{id: "2.2", title: "file-overlap detector", status: state.StatusPending, deps: []string{"1.2"}},
	{id: "3.1", title: "sqlite store init", status: state.StatusBlocked, deps: []string{"2.1"}},
	{id: "3.2", title: "stories CRUD adapter", status: state.StatusPending, deps: []string{"2.2"}},
	{id: "4.1", title: "story-next deps", status: state.StatusPending, deps: []string{"3.2"}},
	{id: "4.2", title: "story-complete ledger", status: state.StatusPending, deps: []string{"3.2", "4.1"}},
}

// seedGraphFixture writes the 4-epic fixture to a fresh sqlite DB and
// returns a fully-wired builder.
func seedGraphFixture(t *testing.T) *sprint.GraphBuilder {
	t.Helper()
	db := openDB(t)
	storiesStore := sqlite.NewStoriesStore(db)
	depsStore := sqlite.NewStoryDependenciesStore(db)
	ctx := context.Background()
	now := time.Now()
	for _, fs := range graphFixture {
		s := state.Story{
			ID:        fs.id,
			Title:     fs.title,
			Status:    fs.status,
			CreatedAt: now,
			UpdatedAt: now,
			Complexity: state.ComplexityMedium,
			StoryType:  state.StoryTypeCode,
		}
		if err := storiesStore.Insert(ctx, s); err != nil {
			t.Fatalf("seed story %s: %v", fs.id, err)
		}
		for _, dep := range fs.deps {
			if err := depsStore.Add(ctx, fs.id, dep); err != nil {
				t.Fatalf("seed dep %s->%s: %v", fs.id, dep, err)
			}
		}
	}
	return &sprint.GraphBuilder{Stories: storiesStore, Dependencies: depsStore}
}

// ----------------------------------------------------------------------------
// Builder behaviour
// ----------------------------------------------------------------------------

func TestGraphBuilder_LoadsAllStoriesAndEdges(t *testing.T) {
	t.Parallel()
	b := seedGraphFixture(t)
	g, err := b.Build(context.Background(), sprint.GraphBuilderOptions{IncludeCompleted: true})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if got, want := len(g.Nodes), len(graphFixture); got != want {
		t.Fatalf("nodes = %d, want %d", got, want)
	}
	// Sum of all fixture deps.
	want := 0
	for _, f := range graphFixture {
		want += len(f.deps)
	}
	if got := len(g.Edges); got != want {
		t.Fatalf("edges = %d, want %d", got, want)
	}
}

// TestGraphBuilder_ScopeIncludesTransitiveUpstream verifies AC4 — `--scope 3`
// must surface every transitive upstream node (here 2.1 + 2.2 + 1.1 + 1.2)
// even though those don't match the "3.*" prefix.
func TestGraphBuilder_ScopeIncludesTransitiveUpstream(t *testing.T) {
	t.Parallel()
	b := seedGraphFixture(t)
	g, err := b.Build(context.Background(), sprint.GraphBuilderOptions{
		Scope:            "3",
		IncludeCompleted: true,
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	ids := nodeIDs(g)
	for _, want := range []string{"1.1", "1.2", "2.1", "2.2", "3.1", "3.2"} {
		if !containsStr(ids, want) {
			t.Errorf("scope=3 graph missing %q (got %v)", want, ids)
		}
	}
	// 4.* must NOT be present — they're downstream, not part of Epic 3
	// or its upstream chain.
	for _, none := range []string{"4.1", "4.2"} {
		if containsStr(ids, none) {
			t.Errorf("scope=3 graph leaked downstream node %q (got %v)", none, ids)
		}
	}
}

// TestGraphBuilder_IncludeCompletedFalse drops green nodes.
func TestGraphBuilder_IncludeCompletedFalse(t *testing.T) {
	t.Parallel()
	b := seedGraphFixture(t)
	g, err := b.Build(context.Background(), sprint.GraphBuilderOptions{IncludeCompleted: false})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	ids := nodeIDs(g)
	for _, none := range []string{"1.1", "1.2"} {
		if containsStr(ids, none) {
			t.Errorf("IncludeCompleted=false leaked complete node %q", none)
		}
	}
}

// TestGraphBuilder_DeterministicOrdering — same input = same output.
func TestGraphBuilder_DeterministicOrdering(t *testing.T) {
	t.Parallel()
	b := seedGraphFixture(t)
	a, err := b.Build(context.Background(), sprint.GraphBuilderOptions{IncludeCompleted: true})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	c, err := b.Build(context.Background(), sprint.GraphBuilderOptions{IncludeCompleted: true})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !equalNodes(a.Nodes, c.Nodes) {
		t.Fatalf("node order not deterministic across two builds")
	}
	if !equalEdges(a.Edges, c.Edges) {
		t.Fatalf("edge order not deterministic across two builds")
	}
}

// ----------------------------------------------------------------------------
// Format / renderer snapshots
// ----------------------------------------------------------------------------

func TestParseFormat(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want sprint.Format
		err  bool
	}{
		{"dot", sprint.FormatDOT, false},
		{"", sprint.FormatDOT, false},
		{"DOT", sprint.FormatDOT, false},
		{"mermaid", sprint.FormatMermaid, false},
		{"json", sprint.FormatJSON, false},
		{"svg", "", true},
	}
	for _, tc := range cases {
		got, err := sprint.ParseFormat(tc.in)
		if tc.err {
			if err == nil {
				t.Errorf("ParseFormat(%q) want error, got %v", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseFormat(%q) unexpected error: %v", tc.in, err)
		}
		if got != tc.want {
			t.Errorf("ParseFormat(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// renderToString builds a 4-epic graph + renders it to a string with the
// chosen format. Shared helper for the three snapshot tests.
func renderToString(t *testing.T, format sprint.Format, withStatus bool, opts sprint.GraphBuilderOptions) string {
	t.Helper()
	b := seedGraphFixture(t)
	g, err := b.Build(context.Background(), opts)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	r, err := sprint.NewRenderer(format, withStatus)
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}
	var buf bytes.Buffer
	if err := r.Render(&buf, g); err != nil {
		t.Fatalf("Render: %v", err)
	}
	return buf.String()
}

// TestRenderDOT_Snapshot — full-fixture DOT output. We assert on the
// structurally-significant substrings rather than the entire string so a
// cosmetic tweak (e.g. switching colour) only updates one line.
func TestRenderDOT_Snapshot(t *testing.T) {
	t.Parallel()
	out := renderToString(t, sprint.FormatDOT, true, sprint.GraphBuilderOptions{IncludeCompleted: true})
	mustContainAll(t, out, []string{
		"digraph sprint {",
		"rankdir=LR;",
		`"1.1" [label="1.1 epics scaffold parser"`,
		`fillcolor="#90EE90"`, // complete = green
		`fillcolor="#FFD580"`, // in-progress = yellow
		`fillcolor="#FF9999"`, // blocked = red
		`fillcolor="#D3D3D3"`, // pending = grey
		`"2.1" -> "1.1";`,
		`"2.1" -> "1.2";`,
		`"4.2" -> "3.2";`,
		`"4.2" -> "4.1";`,
	})
	mustNotContain(t, out, "style=dashed") // no epic-synth edges in fixture
}

// TestRenderDOT_WithStatusFalse omits status colours — every node falls
// back to white. Verifies the toggle actually works.
func TestRenderDOT_WithStatusFalse(t *testing.T) {
	t.Parallel()
	out := renderToString(t, sprint.FormatDOT, false, sprint.GraphBuilderOptions{IncludeCompleted: true})
	mustContainAll(t, out, []string{`fillcolor="#FFFFFF"`})
	mustNotContain(t, out, `#90EE90`)
}

// TestRenderMermaid_Snapshot verifies GitHub-renderable Mermaid output.
func TestRenderMermaid_Snapshot(t *testing.T) {
	t.Parallel()
	out := renderToString(t, sprint.FormatMermaid, true, sprint.GraphBuilderOptions{IncludeCompleted: true})
	mustContainAll(t, out, []string{
		"graph TD",
		`s1_1["1.1 epics scaffold parser"]`,
		`s2_1["2.1 topo-sort batcher"]`,
		`s2_1 --> s1_1`,
		`s4_2 --> s4_1`,
		"classDef complete fill:#90EE90",
		"classDef inflight fill:#FFD580",
		"classDef blocked fill:#FF9999",
		"classDef pending fill:#D3D3D3",
		"class s1_1 complete",
		"class s3_1 blocked",
	})
	// Mermaid epic-synth dashed-arrow form is `-.->`; not present in fixture.
	mustNotContain(t, out, "-.->")
}

// TestRenderJSON_EnvelopeV1 documents the JSON envelope contract (AC3).
func TestRenderJSON_EnvelopeV1(t *testing.T) {
	t.Parallel()
	out := renderToString(t, sprint.FormatJSON, false, sprint.GraphBuilderOptions{IncludeCompleted: true})

	var env sprint.JSONGraphEnvelope
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("envelope did not round-trip: %v\noutput=%s", err, out)
	}
	if env.SchemaVersion != sprint.JSONGraphSchemaVersion {
		t.Errorf("schema_version = %q, want %q", env.SchemaVersion, sprint.JSONGraphSchemaVersion)
	}
	if env.NodeCount != len(graphFixture) {
		t.Errorf("node_count = %d, want %d", env.NodeCount, len(graphFixture))
	}
	if got := env.EdgeCount; got != 8 {
		t.Errorf("edge_count = %d, want 8", got)
	}
	// Validate the per-node fields cover everything a downstream visualiser
	// would need: id, label, status, kind, epic_id, subject.
	for _, n := range env.Nodes {
		if n.ID == "" || n.Label == "" || n.Kind == "" || n.EpicID == "" {
			t.Errorf("node missing required field: %+v", n)
		}
	}
	for _, e := range env.Edges {
		if e.From == "" || e.To == "" || e.Kind == "" {
			t.Errorf("edge missing required field: %+v", e)
		}
	}
}

// TestRenderJSON_EmptyGraph — empty input must produce `[]` not `null` for
// nodes + edges so jq queries don't crash on null-iteration.
func TestRenderJSON_EmptyGraph(t *testing.T) {
	t.Parallel()
	db := openDB(t)
	b := &sprint.GraphBuilder{
		Stories:      sqlite.NewStoriesStore(db),
		Dependencies: sqlite.NewStoryDependenciesStore(db),
	}
	g, err := b.Build(context.Background(), sprint.GraphBuilderOptions{IncludeCompleted: true})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	r := &sprint.JSONRenderer{}
	var buf bytes.Buffer
	if err := r.Render(&buf, g); err != nil {
		t.Fatalf("Render: %v", err)
	}
	mustContainAll(t, buf.String(), []string{
		`"nodes": []`,
		`"edges": []`,
		`"node_count": 0`,
	})
}

// TestRenderer_NilGraph — every renderer must reject nil rather than panic.
func TestRenderer_NilGraph(t *testing.T) {
	t.Parallel()
	for _, format := range []sprint.Format{sprint.FormatDOT, sprint.FormatMermaid, sprint.FormatJSON} {
		r, err := sprint.NewRenderer(format, true)
		if err != nil {
			t.Fatalf("NewRenderer(%v): %v", format, err)
		}
		if err := r.Render(&bytes.Buffer{}, nil); err == nil {
			t.Errorf("renderer(%v).Render(nil) want error", format)
		}
	}
}

// TestTruncate_SubjectLimit covers the 30-char clip rule (AC: label = id +
// first 30 chars of subject).
func TestRenderDOT_SubjectClippedAt30Chars(t *testing.T) {
	t.Parallel()
	db := openDB(t)
	storiesStore := sqlite.NewStoriesStore(db)
	depsStore := sqlite.NewStoryDependenciesStore(db)
	long := strings.Repeat("a", 60)
	now := time.Now()
	if err := storiesStore.Insert(context.Background(), state.Story{
		ID: "9.9", Title: long, Status: state.StatusPending,
		Complexity: state.ComplexityMedium, StoryType: state.StoryTypeCode,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	b := &sprint.GraphBuilder{Stories: storiesStore, Dependencies: depsStore}
	g, _ := b.Build(context.Background(), sprint.GraphBuilderOptions{IncludeCompleted: true})
	if len(g.Nodes) != 1 {
		t.Fatalf("expect 1 node, got %d", len(g.Nodes))
	}
	// 29 'a' chars + "…" = 30 runes total.
	wantSubject := strings.Repeat("a", 29) + "…"
	if g.Nodes[0].Subject != wantSubject {
		t.Errorf("Subject = %q, want %q", g.Nodes[0].Subject, wantSubject)
	}
}

// ----------------------------------------------------------------------------
// helpers
// ----------------------------------------------------------------------------

func nodeIDs(g *sprint.Graph) []string {
	out := make([]string, 0, len(g.Nodes))
	for _, n := range g.Nodes {
		out = append(out, n.ID)
	}
	return out
}

func containsStr(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

func equalNodes(a, b []sprint.Node) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func equalEdges(a, b []sprint.Edge) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func mustContainAll(t *testing.T, haystack string, needles []string) {
	t.Helper()
	for _, needle := range needles {
		if !strings.Contains(haystack, needle) {
			t.Errorf("output missing substring %q\n--- full output ---\n%s", needle, haystack)
		}
	}
}

func mustNotContain(t *testing.T, haystack, needle string) {
	t.Helper()
	if strings.Contains(haystack, needle) {
		t.Errorf("output unexpectedly contains %q", needle)
	}
}
