package cmd

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.uber.org/zap"

	"github.com/sosalejandro/bmad-story-runner-cli/domain/state"
	"github.com/sosalejandro/bmad-story-runner-cli/infrastructure/state/sqlite"
)

// Issue #71: `bmad story context-bundle <id>` regression — the success
// path used to dump JSON-only to stdout (no human-readable confirmation)
// and the not-found path dumped the full cobra usage block, which when
// truncated by a small terminal capture looked like "command silently
// printed nothing and exited". These integration tests pin both the
// happy-path file-is-actually-written contract and the no-usage-spam
// failure contract so a future refactor can't silently drift back.
//
// We exercise the wired cobra tree (NewRootCmd) end-to-end rather than
// calling BuildStoryContext directly — the package-internal helper has
// its own unit tests in application/sprint/storycontext_test.go. The cmd
// layer's job is the flag wiring, default-path resolution from
// docs_folder config, and the output-shape contract; this test pins
// exactly that.

// setupContextBundleProject creates a tmp project with:
//   - bmad-state.db at the project root
//   - docs/epics.md containing one story (1.1) with an FR ref
//   - docs/architecture.md containing the matching FR section
//   - docs_folder config row pointing at the docs/ dir
//
// Returns the project root (cwd-for-the-test) and the resolved db path.
func setupContextBundleProject(t *testing.T) (string, string) {
	t.Helper()

	root := t.TempDir()
	docs := filepath.Join(root, "docs")
	if err := os.MkdirAll(docs, 0o755); err != nil {
		t.Fatalf("mkdir docs: %v", err)
	}

	epicsBody := `# Epics

## Epic 1: Foundation

### Story 1.1: Pick BC

Pick the reference bounded context.

- **Refs:** FR-Arch-7
`
	if err := os.WriteFile(filepath.Join(docs, "epics.md"), []byte(epicsBody), 0o644); err != nil {
		t.Fatalf("write epics.md: %v", err)
	}
	archBody := `# Architecture

### FR-Arch-7 — Reference BC

Body of the FR-Arch-7 section.
`
	if err := os.WriteFile(filepath.Join(docs, "architecture.md"), []byte(archBody), 0o644); err != nil {
		t.Fatalf("write architecture.md: %v", err)
	}

	dbPath := filepath.Join(root, "bmad-state.db")
	ctx := context.Background()
	db, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if err := sqlite.NewConfigStore(db).Set(ctx, "docs_folder", docs); err != nil {
		t.Fatalf("config docs_folder: %v", err)
	}
	if err := sqlite.NewStoriesStore(db).Insert(ctx, state.Story{
		ID:         "1.1",
		Title:      "Pick BC",
		Status:     state.StatusPending,
		Complexity: state.ComplexityLow,
		StoryType:  state.StoryTypeCode,
	}); err != nil {
		t.Fatalf("insert story: %v", err)
	}

	return root, dbPath
}

// runContextBundleCmd invokes the wired root command with the supplied
// args, captures stdout + stderr, and returns them along with the
// Execute() error. cwd is temporarily chdir'd to the project root so the
// default `_bmad-output/context-bundles/<id>.md` resolves under it.
func runContextBundleCmd(t *testing.T, root, dbPath string, args ...string) (string, string, error) {
	t.Helper()

	// Snapshot + restore package globals so parallel-friendly tests
	// don't bleed flag state between cases.
	prevJSON := jsonOutput
	prevState := v6StatePathFlag
	prevNoLog := noLog
	t.Cleanup(func() {
		jsonOutput = prevJSON
		v6StatePathFlag = prevState
		noLog = prevNoLog
	})

	origCwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(origCwd) })

	t.Setenv("BMAD_STATE", dbPath)

	// Disable the audit-log stream tap from the test driver too — its
	// pipe goroutines would otherwise consume the bytes we want to
	// inspect here. (See runWithLogging in root.go.)
	disableStreamTap = true
	t.Cleanup(func() { disableStreamTap = false })

	rootCmd := NewRootCmd(zap.NewNop())

	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe stdout: %v", err)
	}
	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe stderr: %v", err)
	}
	origStdout, origStderr := os.Stdout, os.Stderr
	os.Stdout = stdoutW
	os.Stderr = stderrW

	full := append([]string{"--no-log"}, args...)
	rootCmd.SetArgs(full)
	// Swallow cobra's own Err writes (they go to root.SetErr by default
	// = os.Stderr; redirect to /dev/null-ish so our tap captures only
	// the command's own output, not cobra's "Error: …" prefix we want
	// to keep though). Actually we DO want cobra's Error: line on
	// stderr so the test can assert on it; leave the default.
	_ = rootCmd

	execErr := rootCmd.Execute()

	stdoutW.Close()
	stderrW.Close()
	os.Stdout = origStdout
	os.Stderr = origStderr

	var stdoutBuf, stderrBuf bytes.Buffer
	_, _ = io.Copy(&stdoutBuf, stdoutR)
	_, _ = io.Copy(&stderrBuf, stderrR)

	return stdoutBuf.String(), stderrBuf.String(), execErr
}

// Happy path: bundle is written to the documented default path with
// non-empty content, stderr carries the human-readable confirmation,
// stdout carries the machine-readable JSON summary, and Execute returns
// nil.
func TestStoryContextBundle_HappyPath_WritesFileAndPrintsConfirmation(t *testing.T) {
	root, dbPath := setupContextBundleProject(t)

	stdoutS, stderrS, err := runContextBundleCmd(t, root, dbPath, "story", "context-bundle", "1.1")
	if err != nil {
		t.Fatalf("Execute returned err: %v\nstdout:%s\nstderr:%s", err, stdoutS, stderrS)
	}

	wantPath := filepath.Join(root, "_bmad-output", "context-bundles", "1.1.md")
	info, statErr := os.Stat(wantPath)
	if statErr != nil {
		t.Fatalf("expected bundle at %s, stat err: %v\nstdout:%s\nstderr:%s",
			wantPath, statErr, stdoutS, stderrS)
	}
	if info.Size() == 0 {
		t.Fatalf("bundle %s exists but is empty (regression — close-error swallowing)", wantPath)
	}

	body, _ := os.ReadFile(wantPath)
	if !strings.Contains(string(body), "Story 1.1: Pick BC") {
		t.Errorf("bundle missing the story header; got first 200B:\n%s",
			truncateForTest(string(body)))
	}
	if !strings.Contains(string(body), "FR-Arch-7 — Reference BC") {
		t.Errorf("bundle missing the FR-Arch-7 arch section; got:\n%s",
			truncateForTest(string(body)))
	}

	// Stderr must include the human-readable "bundle written to …" line
	// — this is the issue-#71 regression-guard: in v0.5.0 the only
	// output was a JSON blob on stdout, easy to miss when a terminal
	// capture truncated it.
	if !strings.Contains(stderrS, "bundle written to ") {
		t.Errorf("missing 'bundle written to' confirmation on stderr; got: %q", stderrS)
	}
	if !strings.Contains(stderrS, wantPath) {
		t.Errorf("confirmation must include absolute path %q; got: %q", wantPath, stderrS)
	}

	// Stdout still carries the JSON summary for callers that pipe to jq.
	if !strings.Contains(stdoutS, `"story_id":"1.1"`) {
		t.Errorf("stdout JSON summary missing story_id; got: %q", stdoutS)
	}
}

// Not-found path: command must exit non-zero with a clear error message
// and MUST NOT dump the full cobra usage block (that's the original
// issue-#71 symptom that made the failure look like silent help-text).
func TestStoryContextBundle_StoryNotFound_ExitsNonZeroWithCleanError(t *testing.T) {
	root, dbPath := setupContextBundleProject(t)

	stdoutS, stderrS, err := runContextBundleCmd(t, root, dbPath, "story", "context-bundle", "99.99")
	if err == nil {
		t.Fatalf("expected non-nil error for missing story\nstdout:%s\nstderr:%s", stdoutS, stderrS)
	}

	// Error message must name the missing id so operators don't have to
	// guess what failed.
	if !strings.Contains(err.Error(), "99.99") {
		t.Errorf("error must mention story id 99.99; got: %v", err)
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error must mention 'not found'; got: %v", err)
	}

	// Bundle file must NOT have been created.
	noBundle := filepath.Join(root, "_bmad-output", "context-bundles", "99.99.md")
	if _, statErr := os.Stat(noBundle); !os.IsNotExist(statErr) {
		t.Errorf("bundle %s should not exist on not-found path; stat=%v", noBundle, statErr)
	}

	// SilenceUsage guard: the cobra usage block has a stable "Usage:"
	// header followed by the flag list — if it leaked back in, this
	// substring would appear in stderr.
	if strings.Contains(stderrS, "Usage:\n  bmad story context-bundle") {
		t.Errorf("regression: cobra usage block leaked on runtime error\nstderr:%s", stderrS)
	}
}

func truncateForTest(s string) string {
	const max = 200
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
