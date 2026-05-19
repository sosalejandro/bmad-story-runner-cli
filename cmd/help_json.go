package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// helpJSONFlag is the persistent --help-json toggle. When set, the root
// command emits its full command tree as JSON and exits 0 — without
// running any command. This is the machine-readable counterpart to the
// usual `--help`, intended for AI orchestrators that need to discover
// the entire surface (commands + flags + aliases) without scraping
// the human-formatted help text.
//
// The shape (helpTreeNode) is part of the AI-agent contract:
//
//   - schema_version pinned at the wrapper level (matches --json v1)
//   - commands recurse via .subcommands (never .Commands; lowercase
//     fields for jq friendliness)
//   - flags split into local and persistent so an agent can tell
//     whether a flag also applies to descendants
//
// Adding new fields is non-breaking; removing or renaming requires a
// schema_version bump.
var helpJSONFlag bool

// helpTreeRoot wraps the command tree with the same v1 envelope shape
// the rest of the CLI uses, so consumers can write one parser.
type helpTreeRoot struct {
	SchemaVersion string       `json:"schema_version"`
	Bin           string       `json:"bin"`
	Version       string       `json:"version"`
	Commit        string       `json:"commit"`
	BuildDate     string       `json:"build_date"`
	Tree          helpTreeNode `json:"tree"`
}

type helpTreeNode struct {
	Name        string         `json:"name"`
	Use         string         `json:"use"`
	Short       string         `json:"short"`
	Long        string         `json:"long,omitempty"`
	Aliases     []string       `json:"aliases,omitempty"`
	Hidden      bool           `json:"hidden,omitempty"`
	Deprecated  string         `json:"deprecated,omitempty"`
	Flags       []helpFlag     `json:"flags,omitempty"`
	Persistent  []helpFlag     `json:"persistent_flags,omitempty"`
	Subcommands []helpTreeNode `json:"subcommands,omitempty"`
	Examples    string         `json:"examples,omitempty"`
}

type helpFlag struct {
	Name       string `json:"name"`
	Shorthand  string `json:"shorthand,omitempty"`
	Usage      string `json:"usage"`
	Type       string `json:"type"`
	Default    string `json:"default,omitempty"`
	Deprecated string `json:"deprecated,omitempty"`
	Hidden     bool   `json:"hidden,omitempty"`
}

// registerHelpJSONFlag wires --help-json on the root cobra command.
// Called once from NewRootCmd. We intercept via two hooks:
//
//  1. PersistentPreRunE — fires on any subcommand invocation
//     (`bmad doctor --help-json`, `bmad story status --help-json`, ...)
//  2. The root command's own RunE — fires on bare `bmad --help-json`
//     (no subcommand), which otherwise falls through to cobra's
//     auto-generated help printer.
//
// IMPORTANT: this MUST be called AFTER all subcommands have been
// AddCommand'd, otherwise the tree walk sees an incomplete root.
func registerHelpJSONFlag(root *cobra.Command) {
	root.PersistentFlags().BoolVar(&helpJSONFlag, "help-json", false,
		"emit the full command tree (commands, flags, aliases) as JSON and exit")

	// Hook 1: chain into any existing PersistentPreRunE so we don't
	// trample behavior added elsewhere.
	prior := root.PersistentPreRunE
	root.PersistentPreRunE = func(c *cobra.Command, args []string) error {
		if helpJSONFlag {
			return emitHelpJSON(root, os.Stdout)
		}
		if prior != nil {
			return prior(c, args)
		}
		return nil
	}

	// Hook 2: handle bare `bmad --help-json`. Setting RunE on root
	// shifts cobra's default-help-on-empty-args behavior, so we keep
	// the old behavior intact when the flag is NOT set.
	priorRunE := root.RunE
	priorRun := root.Run
	root.RunE = func(c *cobra.Command, args []string) error {
		if helpJSONFlag {
			return emitHelpJSON(root, os.Stdout)
		}
		if priorRunE != nil {
			return priorRunE(c, args)
		}
		if priorRun != nil {
			priorRun(c, args)
			return nil
		}
		// Preserve cobra's default behavior: print help when no
		// subcommand and no flag was actionable.
		return c.Help()
	}
}

// emitHelpJSON serializes the command tree rooted at `root` and writes
// it to `out`. After a successful write we os.Exit(0) so cobra does
// not also try to run the requested command (with potentially-missing
// args). This matches the precedent set by cobra's own --version flag.
func emitHelpJSON(root *cobra.Command, out *os.File) error {
	tree := buildTree(root)
	wrap := helpTreeRoot{
		SchemaVersion: schemaVersion,
		Bin:           root.Name(),
		Version:       Version,
		Commit:        CommitSHA,
		BuildDate:     BuildDate,
		Tree:          tree,
	}
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	if err := enc.Encode(wrap); err != nil {
		return fmt.Errorf("emit help-json: %w", err)
	}
	// Skip the rest of cobra's pipeline. Returning a sentinel error
	// would surface on stderr; clean exit is what `--version` does too.
	os.Exit(0)
	return nil
}

func buildTree(c *cobra.Command) helpTreeNode {
	n := helpTreeNode{
		Name:       c.Name(),
		Use:        c.Use,
		Short:      c.Short,
		Long:       c.Long,
		Aliases:    c.Aliases,
		Hidden:     c.Hidden,
		Deprecated: c.Deprecated,
		Examples:   c.Example,
	}

	n.Flags = collectFlags(c.NonInheritedFlags())
	n.Persistent = collectFlags(c.PersistentFlags())

	subs := c.Commands()
	// Stable order — cobra orders by AddCommand insertion; we sort by
	// Name() so jq queries are deterministic across builds.
	sort.SliceStable(subs, func(i, j int) bool { return subs[i].Name() < subs[j].Name() })
	for _, s := range subs {
		if s.Name() == "help" {
			continue // cobra's auto-generated help; not part of the agent surface
		}
		n.Subcommands = append(n.Subcommands, buildTree(s))
	}
	return n
}

func collectFlags(fs *pflag.FlagSet) []helpFlag {
	var out []helpFlag
	fs.VisitAll(func(f *pflag.Flag) {
		out = append(out, helpFlag{
			Name:       f.Name,
			Shorthand:  f.Shorthand,
			Usage:      f.Usage,
			Type:       f.Value.Type(),
			Default:    f.DefValue,
			Deprecated: f.Deprecated,
			Hidden:     f.Hidden,
		})
	})
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
