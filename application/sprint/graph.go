package sprint

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/sosalejandro/bmad-story-runner-cli/domain/state"
)

// ============================================================================
// Issue #47 — `bmad sprint graph` dependency-DAG visualisation
// ============================================================================
//
// Reads the resolved edges table (state.StoryDependencies) plus the stories
// table (state.Stories), assembles an in-memory Graph, and renders it as
// DOT, Mermaid, or JSON.
//
// The Builder + Graph + Renderer split is deliberate (SOLID):
//   - Builder OWNS port IO and scope-filter logic; testable via mocks but
//     in practice tested end-to-end via the sqlite adapter.
//   - Graph is the pure data structure — no ports, no IO.
//   - Renderer is an interface; each format is its own struct so adding a
//     fourth format (issue #47 explicitly mentions "future dashboards")
//     touches only one new file, not the Builder or any existing renderer.
//
// Edge-kind semantics: the resolved-edges port currently surfaces only
// story-level depends_on edges (EdgeKindDepends, solid). Issue #46 will
// introduce epic-level requires_epics synthesised edges (EdgeKindEpicSynth,
// dashed); when that ships, the Builder will start to see EdgeKindEpicSynth
// values without any renderer change.
// ============================================================================

// EdgeKind classifies a dependency edge so renderers can style it.
type EdgeKind string

const (
	// EdgeKindDepends is a story-level depends_on edge — solid line.
	EdgeKindDepends EdgeKind = "depends_on"
	// EdgeKindEpicSynth is an epic-level requires_epics edge synthesised by
	// the epic-DAG resolver (issue #46) — dashed line.
	EdgeKindEpicSynth EdgeKind = "epic_synth"
)

// NodeKind classifies a graph node — story vs epic-summary cluster.
type NodeKind string

const (
	NodeKindStory NodeKind = "story"
	NodeKindEpic  NodeKind = "epic"
)

// Node is one vertex in the dependency DAG.
type Node struct {
	ID      string       `json:"id"`
	Label   string       `json:"label"`
	Status  state.Status `json:"status,omitempty"`
	Kind    NodeKind     `json:"kind"`
	EpicID  string       `json:"epic_id,omitempty"`
	Subject string       `json:"subject,omitempty"`
}

// Edge is one directed dependency: From depends on To (so To must complete first).
type Edge struct {
	From string   `json:"from"`
	To   string   `json:"to"`
	Kind EdgeKind `json:"kind"`
}

// Graph is the pure-data DAG passed between Builder and Renderers.
type Graph struct {
	Nodes []Node `json:"nodes"`
	Edges []Edge `json:"edges"`
	// Scope, when non-empty, records the --scope filter that produced this graph.
	Scope string `json:"scope,omitempty"`
}

// SubjectLimit caps the trailing label subject at 30 chars (issue #47
// acceptance: "<id> + first 30 chars of subject").
const SubjectLimit = 30

// ----------------------------------------------------------------------------
// Builder
// ----------------------------------------------------------------------------

// GraphBuilderOptions parameterises a Builder.Build call.
type GraphBuilderOptions struct {
	// Scope, when non-empty, restricts the graph to stories matching this
	// epic id (per StoryMatchesEpic prefix+dot rule) PLUS all transitive
	// upstream nodes — so an operator running `--scope 7` still sees the
	// 4.12 dependency that gates 7.1.
	Scope string
	// IncludeCompleted controls whether complete stories appear. Defaults
	// to true; set to false to hide green nodes and focus on remaining work.
	IncludeCompleted bool
}

// GraphBuilder assembles a Graph from the resolved edges port and the
// stories port.
type GraphBuilder struct {
	Stories      state.Stories
	Dependencies state.StoryDependencies
}

// Build loads every story + every edge, applies the scope/completed filters,
// and returns a deterministic Graph.
//
// Determinism: nodes are sorted by id; edges are sorted by (from, to, kind).
// This is what makes snapshot tests stable across runs.
func (b *GraphBuilder) Build(ctx context.Context, opts GraphBuilderOptions) (*Graph, error) {
	if b.Stories == nil || b.Dependencies == nil {
		return nil, fmt.Errorf("graph builder: Stories and Dependencies ports are required")
	}
	all, err := b.Stories.List(ctx, state.StoryFilter{})
	if err != nil {
		return nil, fmt.Errorf("graph builder: list stories: %w", err)
	}

	// Index stories by id for O(1) lookup.
	byID := make(map[string]state.Story, len(all))
	for _, s := range all {
		byID[s.ID] = s
	}

	// Collect every edge by iterating each story's depends_on list. We
	// intentionally call .Of per story (not a single bulk read) because the
	// port doesn't expose a bulk variant today, and 190 stories × 1 SELECT
	// each on a local SQLite file is ~10ms in practice — well under the
	// 500ms acceptance budget.
	var rawEdges []Edge
	for _, s := range all {
		deps, err := b.Dependencies.Of(ctx, s.ID)
		if err != nil {
			return nil, fmt.Errorf("graph builder: deps for %q: %w", s.ID, err)
		}
		for _, dep := range deps {
			rawEdges = append(rawEdges, Edge{
				From: s.ID,
				To:   dep,
				// Currently all rows are story-level; epic-synth edges (issue
				// #46) will tag themselves once the resolved-edges schema
				// gains an edge_kind column.
				Kind: EdgeKindDepends,
			})
		}
	}

	// Determine the set of story ids to KEEP.
	keep := selectKeep(all, rawEdges, opts)

	// Build node + edge slices in deterministic order.
	g := &Graph{Scope: opts.Scope}
	for _, s := range all {
		if !keep[s.ID] {
			continue
		}
		g.Nodes = append(g.Nodes, storyToNode(s))
	}
	for _, e := range rawEdges {
		if !keep[e.From] || !keep[e.To] {
			continue
		}
		g.Edges = append(g.Edges, e)
	}

	sort.Slice(g.Nodes, func(i, j int) bool { return g.Nodes[i].ID < g.Nodes[j].ID })
	sort.Slice(g.Edges, func(i, j int) bool {
		if g.Edges[i].From != g.Edges[j].From {
			return g.Edges[i].From < g.Edges[j].From
		}
		if g.Edges[i].To != g.Edges[j].To {
			return g.Edges[i].To < g.Edges[j].To
		}
		return g.Edges[i].Kind < g.Edges[j].Kind
	})
	return g, nil
}

// selectKeep returns the set of story ids that survive the scope +
// completed-filter combination.
//
// Scope semantics (issue #47 AC4): when --scope N is set, include every
// story that matches Epic N AND every transitive upstream node so the
// dependency context is visible — otherwise an operator would see "7.1
// depends on something but the something is missing from the graph."
func selectKeep(all []state.Story, edges []Edge, opts GraphBuilderOptions) map[string]bool {
	keep := make(map[string]bool, len(all))
	if opts.Scope == "" {
		for _, s := range all {
			if !opts.IncludeCompleted && s.Status == state.StatusComplete {
				continue
			}
			keep[s.ID] = true
		}
		return keep
	}

	// Seed: every story that matches the scope.
	for _, s := range all {
		if StoryMatchesEpic(s.ID, opts.Scope) {
			keep[s.ID] = true
		}
	}

	// Transitive upstream walk: an edge (from→to) means "from depends on to."
	// We want every upstream `to` reachable from a seeded `from`.
	adjacency := make(map[string][]string, len(all))
	for _, e := range edges {
		adjacency[e.From] = append(adjacency[e.From], e.To)
	}
	frontier := make([]string, 0, len(keep))
	for id := range keep {
		frontier = append(frontier, id)
	}
	for len(frontier) > 0 {
		id := frontier[len(frontier)-1]
		frontier = frontier[:len(frontier)-1]
		for _, up := range adjacency[id] {
			if !keep[up] {
				keep[up] = true
				frontier = append(frontier, up)
			}
		}
	}

	// IncludeCompleted=false filter applies AFTER transitive expansion. A
	// completed upstream prerequisite still appears unless explicitly hidden,
	// which is the right default — operators want to see "7.1 is blocked
	// because complete 4.12 is its only upstream." When they ask to hide
	// completed, we honour that and drop the green node + any edges it owns.
	if !opts.IncludeCompleted {
		for _, s := range all {
			if s.Status == state.StatusComplete {
				delete(keep, s.ID)
			}
		}
	}
	return keep
}

// storyToNode projects a state.Story into a graph Node — the renderer never
// reads Story fields directly so we can swap the projection without touching
// any format-specific code.
func storyToNode(s state.Story) Node {
	subject := truncate(s.Title, SubjectLimit)
	label := s.ID
	if subject != "" {
		label = s.ID + " " + subject
	}
	return Node{
		ID:      s.ID,
		Label:   label,
		Status:  s.Status,
		Kind:    NodeKindStory,
		EpicID:  EpicIDFromStory(s.ID),
		Subject: subject,
	}
}

// truncate clips s to at most n runes (not bytes) so multi-byte titles don't
// get sliced mid-rune, then appends an ellipsis when the clip actually cut.
func truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n-1]) + "…"
}

// ----------------------------------------------------------------------------
// Renderer interface + format dispatch
// ----------------------------------------------------------------------------

// Format names the three accepted --format values.
type Format string

const (
	FormatDOT     Format = "dot"
	FormatMermaid Format = "mermaid"
	FormatJSON    Format = "json"
)

// ParseFormat normalises a --format flag value, returning a typed Format.
func ParseFormat(s string) (Format, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "dot", "":
		return FormatDOT, nil
	case "mermaid":
		return FormatMermaid, nil
	case "json":
		return FormatJSON, nil
	default:
		return "", fmt.Errorf("graph: unknown --format %q (want one of: dot|mermaid|json)", s)
	}
}

// Renderer is the open/closed extension point — to add a new format,
// implement Render and register in NewRenderer.
type Renderer interface {
	Render(w io.Writer, g *Graph) error
}

// NewRenderer returns the renderer matching the given format. The
// `withStatus` toggle is a renderer-level concern (whether to colour /
// style nodes by their status); it threads in at construction so each
// concrete renderer can wire it once.
func NewRenderer(format Format, withStatus bool) (Renderer, error) {
	switch format {
	case FormatDOT:
		return &DOTRenderer{WithStatus: withStatus}, nil
	case FormatMermaid:
		return &MermaidRenderer{WithStatus: withStatus}, nil
	case FormatJSON:
		return &JSONRenderer{}, nil
	default:
		return nil, fmt.Errorf("graph: no renderer for format %q", format)
	}
}

// ----------------------------------------------------------------------------
// Status → colour / shape mapping
//
// Centralised so DOT and Mermaid share the same truth source — if we
// later add a colour-blind palette toggle, it lives in one place.
// ----------------------------------------------------------------------------

// statusColour returns a hex colour for a status. Returned without the
// leading "#" so the renderer can format it per its syntax (DOT wants
// `"#90EE90"`, Mermaid `fill:#90EE90`).
func statusColour(s state.Status) string {
	switch s {
	case state.StatusComplete:
		return "#90EE90" // light green
	case state.StatusInProgress, state.StatusHydrating, state.StatusReviewing,
		state.StatusCommitting, state.StatusEnvUp, state.StatusEnvDown:
		return "#FFD580" // soft yellow — covers all "in flight" stages
	case state.StatusBlocked:
		return "#FF9999" // soft red
	case state.StatusPending:
		return "#D3D3D3" // grey
	default:
		// Defensive default. A status we don't recognise (e.g. a future
		// `hydrated-pending` introduced post-#47) gets a distinctive blue
		// so it stands out for triage — issue #47's status colour list
		// explicitly calls out "blue=hydrated-pending" as a near-term add.
		return "#9CC0FF"
	}
}

// ----------------------------------------------------------------------------
// DOT renderer
// ----------------------------------------------------------------------------

// DOTRenderer emits Graphviz DOT — the default format. Pipe to
// `dot -Tsvg` for an interactive viewer.
type DOTRenderer struct {
	WithStatus bool
}

// Render writes a DOT document to w. The output is deterministic and
// pipe-safe (no trailing whitespace on attribute lines).
func (r *DOTRenderer) Render(w io.Writer, g *Graph) error {
	if g == nil {
		return fmt.Errorf("DOTRenderer: nil graph")
	}
	var b strings.Builder
	b.WriteString("digraph sprint {\n")
	b.WriteString("  rankdir=LR;\n")
	b.WriteString("  node [shape=box, style=\"rounded,filled\", fontname=\"Helvetica\"];\n")
	b.WriteString("  edge [color=\"#666666\"];\n")
	for _, n := range g.Nodes {
		b.WriteString("  ")
		b.WriteString(dotNodeID(n.ID))
		b.WriteString(" [label=")
		b.WriteString(dotQuote(n.Label))
		if r.WithStatus {
			b.WriteString(", fillcolor=\"")
			b.WriteString(statusColour(n.Status))
			b.WriteString("\"")
		} else {
			b.WriteString(", fillcolor=\"#FFFFFF\"")
		}
		b.WriteString("];\n")
	}
	for _, e := range g.Edges {
		b.WriteString("  ")
		b.WriteString(dotNodeID(e.From))
		b.WriteString(" -> ")
		b.WriteString(dotNodeID(e.To))
		if e.Kind == EdgeKindEpicSynth {
			b.WriteString(" [style=dashed]")
		}
		b.WriteString(";\n")
	}
	b.WriteString("}\n")
	_, err := io.WriteString(w, b.String())
	return err
}

// dotNodeID returns a DOT-safe identifier. Story ids like "1.1" or
// "1.1.payment-method-mgmt" aren't valid bare DOT identifiers (dots have
// port-syntax meaning), so we always quote.
func dotNodeID(id string) string {
	return dotQuote(id)
}

// dotQuote wraps s in double-quotes and escapes any embedded `"` or `\`.
func dotQuote(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return `"` + r.Replace(s) + `"`
}

// ----------------------------------------------------------------------------
// Mermaid renderer
// ----------------------------------------------------------------------------

// MermaidRenderer emits a Mermaid `graph TD` block — paste-ready inside a
// GitHub markdown code fence ```mermaid ... ``` for inline render.
type MermaidRenderer struct {
	WithStatus bool
}

// Render writes a Mermaid document to w.
func (r *MermaidRenderer) Render(w io.Writer, g *Graph) error {
	if g == nil {
		return fmt.Errorf("MermaidRenderer: nil graph")
	}
	var b strings.Builder
	b.WriteString("graph TD\n")
	for _, n := range g.Nodes {
		b.WriteString("  ")
		b.WriteString(mermaidNodeID(n.ID))
		b.WriteString("[")
		b.WriteString(mermaidQuote(n.Label))
		b.WriteString("]\n")
	}
	for _, e := range g.Edges {
		arrow := " --> "
		if e.Kind == EdgeKindEpicSynth {
			// Mermaid uses `-.->` for a dashed/dotted arrow.
			arrow = " -.-> "
		}
		b.WriteString("  ")
		b.WriteString(mermaidNodeID(e.From))
		b.WriteString(arrow)
		b.WriteString(mermaidNodeID(e.To))
		b.WriteString("\n")
	}
	if r.WithStatus {
		writeMermaidClassDefs(&b, g)
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// writeMermaidClassDefs appends classDef declarations + per-node class
// assignments. We declare a class only when at least one node uses it so
// the output stays compact on small graphs.
func writeMermaidClassDefs(b *strings.Builder, g *Graph) {
	used := map[string]bool{}
	for _, n := range g.Nodes {
		used[statusClassName(n.Status)] = true
	}
	// Stable ordering for snapshot tests.
	classes := make([]string, 0, len(used))
	for c := range used {
		classes = append(classes, c)
	}
	sort.Strings(classes)
	for _, c := range classes {
		fmt.Fprintf(b, "  classDef %s fill:%s,stroke:#333,stroke-width:1px;\n",
			c, statusClassColour(c))
	}
	for _, n := range g.Nodes {
		fmt.Fprintf(b, "  class %s %s;\n", mermaidNodeID(n.ID), statusClassName(n.Status))
	}
}

// statusClassName maps a status to a Mermaid-safe class identifier.
func statusClassName(s state.Status) string {
	switch s {
	case state.StatusComplete:
		return "complete"
	case state.StatusBlocked:
		return "blocked"
	case state.StatusPending:
		return "pending"
	case state.StatusInProgress, state.StatusHydrating, state.StatusReviewing,
		state.StatusCommitting, state.StatusEnvUp, state.StatusEnvDown:
		return "inflight"
	default:
		return "other"
	}
}

// statusClassColour returns the fill colour for a class id — mirrors
// statusColour but keyed off the class name so the writer can produce the
// classDef line from already-aggregated class ids.
func statusClassColour(class string) string {
	switch class {
	case "complete":
		return "#90EE90"
	case "inflight":
		return "#FFD580"
	case "blocked":
		return "#FF9999"
	case "pending":
		return "#D3D3D3"
	default:
		return "#9CC0FF"
	}
}

// mermaidNodeID makes a Mermaid-safe identifier — Mermaid disallows `.` and
// `-` in bare node ids, so we replace with `_`. The original id stays
// visible inside the label.
func mermaidNodeID(id string) string {
	r := strings.NewReplacer(".", "_", "-", "_", "/", "_")
	return "s" + r.Replace(id)
}

// mermaidQuote wraps a label in double-quotes (Mermaid's preferred syntax
// when the label contains spaces or punctuation) and escapes any `"`.
func mermaidQuote(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}

// ----------------------------------------------------------------------------
// JSON renderer — envelope v1 contract
//
// The wire-format is deliberately separate from the in-memory Graph: it
// adds a schema_version + counts header so downstream tooling can detect
// schema bumps without re-counting nodes / edges itself.
// ----------------------------------------------------------------------------

// JSONGraphEnvelope is the wire-format for `--format json`. Field order
// is significant (Go marshals struct fields in declaration order); changing
// it constitutes a breaking change — bump SchemaVersion when that happens.
type JSONGraphEnvelope struct {
	SchemaVersion string `json:"schema_version"`
	Scope         string `json:"scope,omitempty"`
	NodeCount     int    `json:"node_count"`
	EdgeCount     int    `json:"edge_count"`
	Nodes         []Node `json:"nodes"`
	Edges         []Edge `json:"edges"`
}

// JSONGraphSchemaVersion is the envelope tag — bump only on breaking changes.
const JSONGraphSchemaVersion = "v1"

// JSONRenderer emits the machine-readable envelope.
type JSONRenderer struct{}

// Render writes the envelope as indented JSON.
func (r *JSONRenderer) Render(w io.Writer, g *Graph) error {
	if g == nil {
		return fmt.Errorf("JSONRenderer: nil graph")
	}
	env := JSONGraphEnvelope{
		SchemaVersion: JSONGraphSchemaVersion,
		Scope:         g.Scope,
		NodeCount:     len(g.Nodes),
		EdgeCount:     len(g.Edges),
		Nodes:         g.Nodes,
		Edges:         g.Edges,
	}
	// Normalise nil slices to empty so the output is `[]` not `null`.
	if env.Nodes == nil {
		env.Nodes = []Node{}
	}
	if env.Edges == nil {
		env.Edges = []Edge{}
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(env)
}
