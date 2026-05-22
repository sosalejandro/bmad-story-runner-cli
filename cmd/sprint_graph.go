package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	appsprint "github.com/sosalejandro/bmad-story-runner-cli/application/sprint"
	"github.com/sosalejandro/bmad-story-runner-cli/infrastructure/state/sqlite"
)

// newSprintGraphCmd wires `bmad sprint graph` (issue #47).
//
// It reads the resolved-edges port (currently fed by `bmad sprint plan`;
// will additionally be fed by `bmad sprint infer-epic-deps` once issue #46
// lands) and renders the dependency DAG as DOT, Mermaid, or JSON.
//
// Why this is a thin shim: all rendering and graph-construction logic
// lives in application/sprint/graph.go. The cobra layer is responsible
// only for flag parsing, DB opening, format dispatch, and stdout/JSON
// envelope plumbing — so the same graph code is reusable from tests,
// from a potential `bmad system-check` hook, and from any future
// in-process consumer (e.g. an MCP server that wants the JSON envelope).
func newSprintGraphCmd() *cobra.Command {
	var (
		formatFlag       string
		scope            string
		withStatus       bool
		includeCompleted bool
	)
	cmd := &cobra.Command{
		Use:   "graph",
		Short: "Render the resolved sprint DAG as DOT, Mermaid, or JSON",
		Long: `Reads the resolved edges (story_dependencies) + stories table and
renders the dependency graph for visual inspection.

  bmad sprint graph                            # DOT to stdout
  bmad sprint graph --format mermaid           # Mermaid (paste into a GitHub MD code block)
  bmad sprint graph --format json              # machine-readable envelope v1
  bmad sprint graph --scope 7                  # Epic 7 + transitive upstream
  bmad sprint graph --include-completed=false  # hide green nodes

Pipe DOT through Graphviz to produce SVG:

  bmad sprint graph | dot -Tsvg > sprint.svg

The graph operates on the *resolved* edges — same source of truth that
` + "`bmad story next`" + ` consumes. Epic-level requires_epics edges (issue
#46) will render as dashed lines once that resolver lands; story-level
depends_on edges render as solid lines today.`,
		RunE: func(c *cobra.Command, args []string) error {
			ctx := context.Background()
			format, err := appsprint.ParseFormat(formatFlag)
			if err != nil {
				return err
			}

			db, err := openV6DB(ctx)
			if err != nil {
				return err
			}
			defer db.Close()

			builder := &appsprint.GraphBuilder{
				Stories:      sqlite.NewStoriesStore(db),
				Dependencies: sqlite.NewStoryDependenciesStore(db),
			}
			g, err := builder.Build(ctx, appsprint.GraphBuilderOptions{
				Scope:            scope,
				IncludeCompleted: includeCompleted,
			})
			if err != nil {
				return err
			}

			// --json (top-level) wins over --format: it wraps the JSON-graph
			// envelope inside the standard v1 envelope so AI agents get a
			// single uniform shape across every bmad command. Operators who
			// want the raw graph envelope use `--format json` without
			// `--json`.
			if jsonOutput {
				result := appsprint.JSONGraphEnvelope{
					SchemaVersion: appsprint.JSONGraphSchemaVersion,
					Scope:         g.Scope,
					NodeCount:     len(g.Nodes),
					EdgeCount:     len(g.Edges),
					Nodes:         normaliseNodes(g.Nodes),
					Edges:         normaliseEdges(g.Edges),
				}
				return emitJSONStdout(commandPathSansRoot(c), map[string]any{
					"format":            string(format),
					"scope":             scope,
					"with_status":       withStatus,
					"include_completed": includeCompleted,
				}, result, nil)
			}

			renderer, err := appsprint.NewRenderer(format, withStatus)
			if err != nil {
				return err
			}
			if err := renderer.Render(os.Stdout, g); err != nil {
				return fmt.Errorf("sprint graph render: %w", err)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&formatFlag, "format", "dot",
		"output format: dot | mermaid | json")
	cmd.Flags().StringVar(&scope, "scope", "",
		"restrict to one epic id (e.g. --scope 7); transitive upstream nodes included")
	cmd.Flags().BoolVar(&withStatus, "with-status", true,
		"colour nodes by their status (set false for monochrome output)")
	cmd.Flags().BoolVar(&includeCompleted, "include-completed", true,
		"include complete stories (set false to focus on remaining work)")
	return cmd
}

// normaliseNodes / normaliseEdges guarantee `[]` (never `null`) inside the
// top-level --json envelope. Mirrors the JSONRenderer's behaviour — the
// renderer normalises before encode; the cobra path bypasses the renderer
// when --json is set, so we normalise here too.
func normaliseNodes(in []appsprint.Node) []appsprint.Node {
	if in == nil {
		return []appsprint.Node{}
	}
	return in
}

func normaliseEdges(in []appsprint.Edge) []appsprint.Edge {
	if in == nil {
		return []appsprint.Edge{}
	}
	return in
}
