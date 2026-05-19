package cmd

import (
	"bufio"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// withIsolatedAuditHome points HOME and the project-log discovery path at a
// fresh temp dir so a test invocation of runWithLogging writes to a known
// location and doesn't touch the developer's real ~/.bmad/audit.jsonl.
//
// Returns (progressPath, projectLog, globalLog).
func withIsolatedAuditHome(t *testing.T) (string, string, string) {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	projectDir := filepath.Join(tmp, "project")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}
	progressPath := filepath.Join(projectDir, "bmad-progress.json")
	if err := os.WriteFile(progressPath, []byte("{}"), 0o644); err != nil {
		t.Fatalf("write progress: %v", err)
	}

	projectLog := filepath.Join(projectDir, "bmad-audit.jsonl")
	globalLog := filepath.Join(tmp, ".bmad", "audit.jsonl")
	return progressPath, projectLog, globalLog
}

func countJSONLLines(t *testing.T, path string) int {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0
		}
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()
	n := 0
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for s.Scan() {
		if len(strings.TrimSpace(s.Text())) == 0 {
			continue
		}
		n++
	}
	if err := s.Err(); err != nil {
		t.Fatalf("scan %s: %v", path, err)
	}
	return n
}

// TestRunWithLogging_SingleEntryPerInvocation locks in the fix for #4: the
// audit log should grow by exactly one row per `bmad` invocation, never two.
// Before the fix runWithLogging emitted a session_start entry plus a command
// entry on every invocation (PID changes across invocations, so the
// "is new session" check always returned true).
func TestRunWithLogging_SingleEntryPerInvocation(t *testing.T) {
	progressPath, projectLog, _ := withIsolatedAuditHome(t)

	c := &cobra.Command{Use: "mark-story-file"}
	if err := runWithLogging(c, []string{progressPath}, func() error { return nil }); err != nil {
		t.Fatalf("runWithLogging: %v", err)
	}
	if got := countJSONLLines(t, projectLog); got != 1 {
		t.Fatalf("after 1 invocation, project log lines = %d, want 1", got)
	}

	// A second invocation must add exactly one more line, not two.
	if err := runWithLogging(c, []string{progressPath}, func() error { return nil }); err != nil {
		t.Fatalf("runWithLogging (second call): %v", err)
	}
	if got := countJSONLLines(t, projectLog); got != 2 {
		t.Fatalf("after 2 invocations, project log lines = %d, want 2", got)
	}
}

// TestRunWithLogging_NoLogFlag verifies the --no-log escape hatch still
// suppresses all audit writes.
func TestRunWithLogging_NoLogFlag(t *testing.T) {
	progressPath, projectLog, globalLog := withIsolatedAuditHome(t)

	prev := noLog
	noLog = true
	t.Cleanup(func() { noLog = prev })

	c := &cobra.Command{Use: "mark-story-file"}
	if err := runWithLogging(c, []string{progressPath}, func() error { return nil }); err != nil {
		t.Fatalf("runWithLogging: %v", err)
	}
	if got := countJSONLLines(t, projectLog); got != 0 {
		t.Fatalf("--no-log: project log lines = %d, want 0", got)
	}
	if got := countJSONLLines(t, globalLog); got != 0 {
		t.Fatalf("--no-log: global log lines = %d, want 0", got)
	}
}

// TestRunWithLogging_RecordsCommandFailure ensures error results still write
// exactly one row (with exit_code=1).
func TestRunWithLogging_RecordsCommandFailure(t *testing.T) {
	progressPath, projectLog, _ := withIsolatedAuditHome(t)

	c := &cobra.Command{Use: "mark-story-file"}
	wantErr := errors.New("boom")
	err := runWithLogging(c, []string{progressPath}, func() error { return wantErr })
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("runWithLogging err = %v, want %q", err, wantErr)
	}
	if got := countJSONLLines(t, projectLog); got != 1 {
		t.Fatalf("on failure, project log lines = %d, want 1", got)
	}
}
