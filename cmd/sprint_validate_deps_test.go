package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.uber.org/zap"
)

// TestSprintValidateDeps_CleanText runs the cobra command end-to-end on a
// clean DAG fixture (single epic, linear chain → no findings) and asserts
// the success path emits an "OK" line on stdout. The success path never
// calls os.Exit(), so cobra Execute returns nil normally.
func TestSprintValidateDeps_CleanText(t *testing.T) {
	dir := t.TempDir()
	epics := filepath.Join(dir, "epics.md")
	if err := os.WriteFile(epics, []byte(`## Epic 1: Slice 0a — Reference

### Story 1.1: Foo

---
story_id: "1.1"
---

### Story 1.2: Bar

---
story_id: "1.2"
depends_on: ["1.1"]
---
`), 0o644); err != nil {
		t.Fatal(err)
	}

	disableStreamTap = true
	t.Cleanup(func() { disableStreamTap = false })

	root := NewRootCmd(zap.NewNop())
	root.SetArgs([]string{"sprint", "validate-deps", "--epics", epics, "--no-log"})

	out := captureStdout(t, func() {
		if err := root.Execute(); err != nil {
			t.Fatalf("Execute: %v", err)
		}
	})

	if !strings.Contains(out, "OK") {
		t.Errorf("expected OK in clean text output, got:\n%s", out)
	}
}

// TestSprintValidateDeps_JSONEnvelope asserts the --json wire shape for the
// success path. We use a clean fixture so the command's blocking-exit branch
// (which calls os.Exit(30) and would terminate the test process) is never
// taken. The blocking-exit + counts behaviour is unit-tested in
// application/sprint/validate_test.go via HasBlockingFindings(), so this
// test only needs to cover the envelope wrapper.
func TestSprintValidateDeps_JSONEnvelope(t *testing.T) {
	dir := t.TempDir()
	epics := filepath.Join(dir, "clean.md")
	if err := os.WriteFile(epics, []byte(`### Story 1.1: Only

---
story_id: "1.1"
---
`), 0o644); err != nil {
		t.Fatal(err)
	}

	disableStreamTap = true
	t.Cleanup(func() { disableStreamTap = false })

	root := NewRootCmd(zap.NewNop())
	root.SetArgs([]string{"sprint", "validate-deps", "--epics", epics, "--no-log", "--json"})

	out := captureStdout(t, func() {
		if err := root.Execute(); err != nil {
			t.Fatalf("Execute: %v", err)
		}
	})

	// Envelope shape mirrors cmd/jsonout.go's jsonEnvelope contract. We
	// duplicate the struct rather than importing the private type, so a
	// breaking rename of those fields is forced to update this assertion
	// as a tripwire.
	type envelope struct {
		SchemaVersion string `json:"schema_version"`
		Command       string `json:"command"`
		Result        struct {
			Findings []struct {
				Kind     string `json:"kind"`
				Severity string `json:"severity"`
			} `json:"findings"`
			Counts struct {
				Error int `json:"error"`
				Warn  int `json:"warn"`
				Info  int `json:"info"`
			} `json:"counts"`
			Scope        string `json:"scope,omitempty"`
			Strict       bool   `json:"strict"`
			TotalStories int    `json:"total_stories"`
		} `json:"result"`
	}

	var env envelope
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("envelope JSON parse failed: %v\nstdout:\n%s", err, out)
	}
	if env.SchemaVersion != "v1" {
		t.Errorf("schema_version = %q, want v1", env.SchemaVersion)
	}
	if env.Command != "sprint validate-deps" {
		t.Errorf("command field = %q, want %q", env.Command, "sprint validate-deps")
	}
	if total := env.Result.Counts.Error + env.Result.Counts.Warn + env.Result.Counts.Info; total != 0 {
		t.Errorf("clean fixture should have zero counts, got %+v", env.Result.Counts)
	}
	if env.Result.TotalStories != 1 {
		t.Errorf("total_stories = %d, want 1", env.Result.TotalStories)
	}
}

// Verify the command registers under the cobra `sprint` namespace so the
// help surface (and `bmad --help-json`) discovers it. The help surface is
// part of the AI-agent ergonomic contract introduced in #38.
func TestSprintValidateDeps_RegisteredInTree(t *testing.T) {
	disableStreamTap = true
	t.Cleanup(func() { disableStreamTap = false })
	root := NewRootCmd(zap.NewNop())
	found := false
	for _, c := range root.Commands() {
		if c.Name() != "sprint" {
			continue
		}
		for _, sub := range c.Commands() {
			if sub.Name() == "validate-deps" {
				found = true
				break
			}
		}
	}
	if !found {
		t.Fatalf("validate-deps not registered under sprint command tree")
	}
}
