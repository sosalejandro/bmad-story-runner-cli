package cmd

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"

	appsprint "github.com/sosalejandro/bmad-story-runner-cli/application/sprint"
	"github.com/sosalejandro/bmad-story-runner-cli/domain/state"
	"github.com/sosalejandro/bmad-story-runner-cli/infrastructure/state/sqlite"
)

// seedGraphFixtureForCmd writes a small 3-story DAG to a DB, returns its path.
// Smaller than the app-layer fixture — we only need enough nodes to verify
// the cobra wire-up reaches the renderer.
func seedGraphFixtureForCmd(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "bmad-state.db")
	ctx := context.Background()
	db, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	stories := sqlite.NewStoriesStore(db)
	deps := sqlite.NewStoryDependenciesStore(db)
	now := time.Now()
	for _, s := range []state.Story{
		{ID: "1.1", Title: "alpha", Status: state.StatusComplete, Complexity: state.ComplexityMedium, StoryType: state.StoryTypeCode, CreatedAt: now, UpdatedAt: now},
		{ID: "1.2", Title: "beta", Status: state.StatusInProgress, Complexity: state.ComplexityMedium, StoryType: state.StoryTypeCode, CreatedAt: now, UpdatedAt: now},
		{ID: "2.1", Title: "gamma", Status: state.StatusPending, Complexity: state.ComplexityMedium, StoryType: state.StoryTypeCode, CreatedAt: now, UpdatedAt: now},
	} {
		if err := stories.Insert(ctx, s); err != nil {
			t.Fatalf("seed %s: %v", s.ID, err)
		}
	}
	if err := deps.Add(ctx, "1.2", "1.1"); err != nil {
		t.Fatal(err)
	}
	if err := deps.Add(ctx, "2.1", "1.2"); err != nil {
		t.Fatal(err)
	}
	return dbPath
}

// TestSprintGraphCmd_DOT — default invocation emits DOT.
func TestSprintGraphCmd_DOT(t *testing.T) {
	dbPath := seedGraphFixtureForCmd(t)
	t.Setenv("BMAD_STATE", dbPath)
	disableStreamTap = true
	t.Cleanup(func() { disableStreamTap = false })

	root := NewRootCmd(zap.NewNop())
	root.SetArgs([]string{"sprint", "graph"})
	out := captureStdout(t, func() {
		if err := root.Execute(); err != nil {
			t.Fatalf("Execute: %v", err)
		}
	})

	for _, want := range []string{
		"digraph sprint {",
		`"1.1" [label=`,
		`"1.2" -> "1.1";`,
		`"2.1" -> "1.2";`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("DOT output missing %q\n--- got ---\n%s", want, out)
		}
	}
}

// TestSprintGraphCmd_Mermaid — --format mermaid emits Mermaid syntax.
func TestSprintGraphCmd_Mermaid(t *testing.T) {
	dbPath := seedGraphFixtureForCmd(t)
	t.Setenv("BMAD_STATE", dbPath)
	disableStreamTap = true
	t.Cleanup(func() { disableStreamTap = false })

	root := NewRootCmd(zap.NewNop())
	root.SetArgs([]string{"sprint", "graph", "--format", "mermaid"})
	out := captureStdout(t, func() {
		if err := root.Execute(); err != nil {
			t.Fatalf("Execute: %v", err)
		}
	})

	for _, want := range []string{"graph TD", "s1_1[", "s1_2 --> s1_1"} {
		if !strings.Contains(out, want) {
			t.Errorf("Mermaid output missing %q\n--- got ---\n%s", want, out)
		}
	}
}

// TestSprintGraphCmd_JSONFormat — --format json emits the JSONGraphEnvelope
// directly (raw envelope, no outer v1-wrapper).
func TestSprintGraphCmd_JSONFormat(t *testing.T) {
	dbPath := seedGraphFixtureForCmd(t)
	t.Setenv("BMAD_STATE", dbPath)
	disableStreamTap = true
	t.Cleanup(func() { disableStreamTap = false })

	root := NewRootCmd(zap.NewNop())
	root.SetArgs([]string{"sprint", "graph", "--format", "json"})
	out := captureStdout(t, func() {
		if err := root.Execute(); err != nil {
			t.Fatalf("Execute: %v", err)
		}
	})

	var env appsprint.JSONGraphEnvelope
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("envelope decode: %v\n%s", err, out)
	}
	if env.SchemaVersion != appsprint.JSONGraphSchemaVersion {
		t.Errorf("schema_version = %q", env.SchemaVersion)
	}
	if env.NodeCount != 3 || env.EdgeCount != 2 {
		t.Errorf("counts = (n=%d, e=%d), want (3, 2)", env.NodeCount, env.EdgeCount)
	}
}

// TestSprintGraphCmd_Scope_TransitiveUpstream — --scope 2 must surface 1.2
// and 1.1 (the transitive upstream of 2.1).
func TestSprintGraphCmd_Scope_TransitiveUpstream(t *testing.T) {
	dbPath := seedGraphFixtureForCmd(t)
	t.Setenv("BMAD_STATE", dbPath)
	disableStreamTap = true
	t.Cleanup(func() { disableStreamTap = false })

	root := NewRootCmd(zap.NewNop())
	root.SetArgs([]string{"sprint", "graph", "--scope", "2", "--format", "json"})
	out := captureStdout(t, func() {
		if err := root.Execute(); err != nil {
			t.Fatalf("Execute: %v", err)
		}
	})

	var env appsprint.JSONGraphEnvelope
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	// Expect 2.1 (scope match) + 1.2 + 1.1 (transitive upstream) = 3 nodes.
	if env.NodeCount != 3 {
		t.Errorf("scope=2 node_count = %d, want 3 (transitive upstream)", env.NodeCount)
	}
}

// TestSprintGraphCmd_InvalidFormat — bad --format value returns an error
// (cobra still exits zero but the RunE error is surfaced; we assert by
// running through root.Execute and checking the returned error).
func TestSprintGraphCmd_InvalidFormat(t *testing.T) {
	dbPath := seedGraphFixtureForCmd(t)
	t.Setenv("BMAD_STATE", dbPath)
	disableStreamTap = true
	t.Cleanup(func() { disableStreamTap = false })

	root := NewRootCmd(zap.NewNop())
	root.SilenceErrors = true
	root.SilenceUsage = true
	root.SetArgs([]string{"sprint", "graph", "--format", "svg"})
	// Discard stdout/stderr noise.
	_ = captureStdout(t, func() {
		err := root.Execute()
		if err == nil {
			t.Fatal("expected error for --format svg, got nil")
		}
		if !strings.Contains(err.Error(), "unknown --format") {
			t.Errorf("unexpected error: %v", err)
		}
	})
}
