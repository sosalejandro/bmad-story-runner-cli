package cmd

import (
	"encoding/json"
	"testing"

	"github.com/spf13/cobra"
)

// TestBuildTreeIncludesKnownCommands verifies that buildTree walks the
// full surface — if a new top-level command is added but not wired into
// the AddCommand block in root.go, this test will still pass; but if
// buildTree itself is broken (e.g. skips subcommands), this catches it.
func TestBuildTreeIncludesKnownCommands(t *testing.T) {
	root := &cobra.Command{Use: "bmad"}
	root.AddCommand(
		&cobra.Command{Use: "doctor"},
		&cobra.Command{Use: "init"},
		&cobra.Command{
			Use: "env",
		},
	)
	envCmd := root.Commands()[0]
	// Find env regardless of order; AddCommand keeps insertion order.
	for _, c := range root.Commands() {
		if c.Name() == "env" {
			envCmd = c
		}
	}
	envCmd.AddCommand(&cobra.Command{Use: "up"})

	tree := buildTree(root)
	if tree.Name != "bmad" {
		t.Fatalf("root name = %q, want bmad", tree.Name)
	}
	names := map[string]bool{}
	for _, sc := range tree.Subcommands {
		names[sc.Name] = true
		if sc.Name == "env" {
			subnames := map[string]bool{}
			for _, ssc := range sc.Subcommands {
				subnames[ssc.Name] = true
			}
			if !subnames["up"] {
				t.Errorf("env subcommands missing 'up': %v", sc.Subcommands)
			}
		}
	}
	for _, want := range []string{"doctor", "init", "env"} {
		if !names[want] {
			t.Errorf("tree.subcommands missing %q (have: %v)", want, names)
		}
	}
}

// TestBuildTreeSkipsAutoHelp ensures cobra's auto-generated "help"
// subcommand is filtered out — agents don't need it in the contract.
func TestBuildTreeSkipsAutoHelp(t *testing.T) {
	root := &cobra.Command{Use: "bmad"}
	root.AddCommand(&cobra.Command{Use: "doctor"})
	// Force cobra to register its auto "help" sub.
	_ = root.Help()

	tree := buildTree(root)
	for _, sc := range tree.Subcommands {
		if sc.Name == "help" {
			t.Errorf("help subcommand should be filtered out of help-json tree")
		}
	}
}

// TestHelpFlagShapeIsStable locks in the JSON field names. Renaming
// these is a breaking change for any downstream agent.
func TestHelpFlagShapeIsStable(t *testing.T) {
	f := helpFlag{Name: "json", Type: "bool", Usage: "emit JSON", Default: "false"}
	b, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	for _, want := range []string{`"name"`, `"type"`, `"usage"`, `"default"`} {
		if !contains(got, want) {
			t.Errorf("helpFlag JSON missing field %s: %s", want, got)
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
