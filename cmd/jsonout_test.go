package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/sosalejandro/bmad-story-runner-cli/domain/state"
	"github.com/sosalejandro/bmad-story-runner-cli/infrastructure/state/sqlite"
	"go.uber.org/zap"
)

// envelopeShape is a permissive decode target — we only assert the
// envelope's framing fields here. Per-command result bodies are decoded
// separately so each test only owns the shape it cares about.
type envelopeShape struct {
	SchemaVersion string          `json:"schema_version"`
	Command       string          `json:"command"`
	Args          map[string]any  `json:"args"`
	Result        json.RawMessage `json:"result"`
	Warnings      []string        `json:"warnings"`
	GeneratedAt   string          `json:"generated_at"`
}

// runCmdJSONCapture seeds an isolated state DB, sets up the root cobra
// command with the --json flag toggled, and captures stdout. Returns the
// decoded envelope plus the raw bytes for additional shape assertions.
//
// We rebuild a fresh root each call (rather than reusing across tests)
// so the global cobra command state doesn't leak between subtests — the
// `--json` persistent flag, the `--state` flag, and the package-level
// `jsonOutput` var would otherwise carry over.
func runCmdJSONCapture(t *testing.T, dbPath string, args ...string) (envelopeShape, []byte) {
	t.Helper()

	// Snapshot + restore globals — package-level state (jsonOutput,
	// v6StatePathFlag, noLog) is captured into root cobra flags during
	// NewRootCmd. Restoring them post-test keeps parallel-friendly.
	prevJSON := jsonOutput
	prevState := v6StatePathFlag
	prevNoLog := noLog
	t.Cleanup(func() {
		jsonOutput = prevJSON
		v6StatePathFlag = prevState
		noLog = prevNoLog
	})

	logger := zap.NewNop()
	root := NewRootCmd(logger)

	// Funnel stdout for the duration of root.Execute. We swap os.Stdout
	// because the commands write via fmt.Fprintln(os.Stdout, ...) — there's
	// no per-command writer to override. Restore in defer so a panicking
	// subcommand doesn't leak the swap.
	origStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w

	// Always inject --no-log + --state so we don't touch ~/.bmad and so
	// the command hits our temp DB.
	full := append([]string{"--no-log", "--state", dbPath, "--json"}, args...)
	root.SetArgs(full)
	// Suppress cobra's own stderr writes during the test — they'd pollute
	// the captured stream. Subcommands that print to stderr (e.g. reaped
	// claim warnings) still print to the real stderr, which is fine here.
	root.SetErr(io.Discard)

	execErr := root.Execute()

	w.Close()
	os.Stdout = origStdout
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)

	if execErr != nil {
		t.Fatalf("Execute(%v): %v\nstdout: %s", full, execErr, buf.String())
	}
	var env envelopeShape
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal envelope: %v\nstdout: %s", err, buf.String())
	}
	return env, buf.Bytes()
}

// seedStoriesForJSON inserts a deterministic set of rows into a fresh
// state DB and returns the resolved path. Used by every --json test.
func seedStoriesForJSON(t *testing.T, stories ...state.Story) string {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "bmad-state.db")
	ctx := context.Background()
	db, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	store := sqlite.NewStoriesStore(db)
	for _, st := range stories {
		if err := store.Insert(ctx, st); err != nil {
			t.Fatalf("seed %s: %v", st.ID, err)
		}
	}
	return dbPath
}

// commonEnvelopeAssertions asserts the framing every --json envelope
// shares. Per-command tests call this once + then unmarshal .Result into
// their command-specific shape.
func commonEnvelopeAssertions(t *testing.T, env envelopeShape, wantCommand string) {
	t.Helper()
	if env.SchemaVersion != "v1" {
		t.Errorf("schema_version = %q, want v1", env.SchemaVersion)
	}
	if env.Command != wantCommand {
		t.Errorf("command = %q, want %q", env.Command, wantCommand)
	}
	if env.GeneratedAt == "" {
		t.Errorf("generated_at empty")
	}
	if env.Warnings == nil {
		t.Errorf("warnings nil (should be []string{} when empty)")
	}
}

// TestJSON_StoryStatusList verifies `bmad story status --json` (no id)
// returns a flat array of story rows under .result.
func TestJSON_StoryStatusList(t *testing.T) {
	dbPath := seedStoriesForJSON(t,
		state.Story{ID: "1.1", Title: "Auth", Status: state.StatusPending, Complexity: state.ComplexityMedium, StoryType: state.StoryTypeCode},
		state.Story{ID: "1.2", Title: "Profile", Status: state.StatusComplete, Complexity: state.ComplexityLow, StoryType: state.StoryTypeCode},
	)

	env, _ := runCmdJSONCapture(t, dbPath, "story", "status")
	commonEnvelopeAssertions(t, env, "story status")

	var rows []storyRowJSON
	if err := json.Unmarshal(env.Result, &rows); err != nil {
		t.Fatalf("unmarshal rows: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
	// Build a quick id -> row index for stable lookup.
	byID := map[string]storyRowJSON{}
	for _, r := range rows {
		byID[r.ID] = r
	}
	if byID["1.1"].Status != "pending" {
		t.Errorf("1.1 status = %s, want pending", byID["1.1"].Status)
	}
	if byID["1.2"].Title != "Profile" {
		t.Errorf("1.2 title = %s, want Profile", byID["1.2"].Title)
	}
}

// TestJSON_StoryStatusDetail verifies `bmad story status <id> --json` returns a
// single object (not an array) with the per-story detail fields.
func TestJSON_StoryStatusDetail(t *testing.T) {
	dbPath := seedStoriesForJSON(t,
		state.Story{ID: "2.1", Title: "Cross-cutting", Status: state.StatusInProgress, Complexity: state.ComplexityHigh, StoryType: state.StoryTypeCode},
	)
	env, _ := runCmdJSONCapture(t, dbPath, "story", "status", "2.1")
	commonEnvelopeAssertions(t, env, "story status")
	var detail map[string]any
	if err := json.Unmarshal(env.Result, &detail); err != nil {
		t.Fatalf("unmarshal detail: %v", err)
	}
	if detail["id"] != "2.1" {
		t.Errorf("id = %v, want 2.1", detail["id"])
	}
	if detail["status"] != "in-progress" {
		t.Errorf("status = %v, want in-progress", detail["status"])
	}
	// args.id should be populated so a JQ query can correlate envelope ←→ invocation.
	if env.Args["id"] != "2.1" {
		t.Errorf("args.id = %v, want 2.1", env.Args["id"])
	}
}

// TestJSON_StoryNext verifies `bmad story next --json` wraps the existing
// raw-JSON output in the v1 envelope while preserving the actions array.
func TestJSON_StoryNext(t *testing.T) {
	dbPath := seedStoriesForJSON(t,
		state.Story{ID: "1.1", Title: "Top", Status: state.StatusPending, Complexity: state.ComplexityMedium, StoryType: state.StoryTypeCode},
	)
	env, _ := runCmdJSONCapture(t, dbPath, "story", "next", "--claim=false")
	commonEnvelopeAssertions(t, env, "story next")
	var result map[string]any
	if err := json.Unmarshal(env.Result, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := result["actions"]; !ok {
		t.Errorf("result missing actions key: %+v", result)
	}
	if _, ok := result["claim_ttl_seconds"]; !ok {
		t.Errorf("result missing claim_ttl_seconds: %+v", result)
	}
}

// TestJSON_StoryHydrate verifies `bmad story hydrate --json` wraps the
// dispatch instruction in the envelope. Story must exist or hydrate
// rejects.
func TestJSON_StoryHydrate(t *testing.T) {
	dbPath := seedStoriesForJSON(t,
		state.Story{ID: "3.1", Title: "x", Status: state.StatusPending, Complexity: state.ComplexityMedium, StoryType: state.StoryTypeCode},
	)
	env, _ := runCmdJSONCapture(t, dbPath, "story", "hydrate", "3.1")
	commonEnvelopeAssertions(t, env, "story hydrate")
	var result map[string]string
	if err := json.Unmarshal(env.Result, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result["action"] != "dispatch" || result["story_id"] != "3.1" {
		t.Errorf("hydrate result wrong: %+v", result)
	}
}

// TestJSON_StoryApplicableStages verifies the per-story stage planner
// emits its ordered slices under .result.{applicable,skipped}.
func TestJSON_StoryApplicableStages(t *testing.T) {
	dbPath := seedStoriesForJSON(t,
		state.Story{ID: "4.1", Title: "x", Status: state.StatusPending, Complexity: state.ComplexityLow, StoryType: state.StoryTypeCode},
	)
	env, _ := runCmdJSONCapture(t, dbPath, "story", "applicable-stages", "4.1")
	commonEnvelopeAssertions(t, env, "story applicable-stages")
	var result map[string]any
	if err := json.Unmarshal(env.Result, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := result["applicable"].([]any); !ok {
		t.Errorf("result.applicable wrong type: %T", result["applicable"])
	}
	if result["story_id"] != "4.1" {
		t.Errorf("result.story_id = %v, want 4.1", result["story_id"])
	}
}

// TestJSON_ConfigGetSet exercises both the read + write paths in one
// flow. Confirms previous_value reflects the prior write.
func TestJSON_ConfigGetSet(t *testing.T) {
	dbPath := seedStoriesForJSON(t)

	// First set — no prior value.
	envSet, _ := runCmdJSONCapture(t, dbPath, "config", "set", "mode", "pragmatic")
	commonEnvelopeAssertions(t, envSet, "config set")
	var setResult map[string]any
	if err := json.Unmarshal(envSet.Result, &setResult); err != nil {
		t.Fatalf("set unmarshal: %v", err)
	}
	if setResult["ok"] != true {
		t.Errorf("set.ok = %v, want true", setResult["ok"])
	}
	if setResult["previous_value"] != nil {
		t.Errorf("set.previous_value = %v, want nil (first write)", setResult["previous_value"])
	}

	// Get returns the set value.
	envGet, _ := runCmdJSONCapture(t, dbPath, "config", "get", "mode")
	commonEnvelopeAssertions(t, envGet, "config get")
	var getResult map[string]any
	if err := json.Unmarshal(envGet.Result, &getResult); err != nil {
		t.Fatalf("get unmarshal: %v", err)
	}
	if getResult["value"] != "pragmatic" {
		t.Errorf("get.value = %v, want pragmatic", getResult["value"])
	}

	// Second set — should report the prior value.
	envSet2, _ := runCmdJSONCapture(t, dbPath, "config", "set", "mode", "strict")
	var set2Result map[string]any
	if err := json.Unmarshal(envSet2.Result, &set2Result); err != nil {
		t.Fatalf("set2 unmarshal: %v", err)
	}
	if set2Result["previous_value"] != "pragmatic" {
		t.Errorf("set2.previous_value = %v, want pragmatic", set2Result["previous_value"])
	}
}

// TestJSON_ConfigAudit verifies the orphan-keys audit emits the expected
// {count, orphans[]} shape when no orphans exist.
func TestJSON_ConfigAudit(t *testing.T) {
	dbPath := seedStoriesForJSON(t)
	env, _ := runCmdJSONCapture(t, dbPath, "config", "audit")
	commonEnvelopeAssertions(t, env, "config audit")
	var result map[string]any
	if err := json.Unmarshal(env.Result, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result["count"].(float64) != 0 {
		t.Errorf("audit clean DB: count = %v, want 0", result["count"])
	}
	if orphans, ok := result["orphans"].([]any); !ok || len(orphans) != 0 {
		t.Errorf("audit clean DB: orphans = %v, want []", result["orphans"])
	}
}

// TestJSON_SprintStatus exercises `bmad sprint status --json`. The DB is
// nearly empty — we just want to confirm the envelope wraps the report
// map without losing keys.
func TestJSON_SprintStatus(t *testing.T) {
	dbPath := seedStoriesForJSON(t,
		state.Story{ID: "1.1", Title: "x", Status: state.StatusPending, Complexity: state.ComplexityLow, StoryType: state.StoryTypeCode},
	)
	env, _ := runCmdJSONCapture(t, dbPath, "sprint", "status")
	commonEnvelopeAssertions(t, env, "sprint status")
	var result map[string]any
	if err := json.Unmarshal(env.Result, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, key := range []string{"by_status", "tokens", "total_stories", "batches"} {
		if _, ok := result[key]; !ok {
			t.Errorf("result missing %q: %+v", key, result)
		}
	}
}
