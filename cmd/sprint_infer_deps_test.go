package cmd

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.uber.org/zap"
)

// TestSprintInferDeps_DryRun runs the cobra command end-to-end on a small
// synthetic epics.md fixture and asserts the stdout contains the expected
// patch suggestion + new dep annotation.
func TestSprintInferDeps_DryRun(t *testing.T) {
	dir := t.TempDir()
	epics := filepath.Join(dir, "epics.md")
	if err := os.WriteFile(epics, []byte(`## Epic 1: Slice 0a — Reference

### Story 1.1: Pick BC

### Story 1.4: Verify task check

## Epic 4: Slice 1 — identity

### Story 4.1: Aggregates

### Story 4.2: Canonical Service

---
story_id: "4.2"
depends_on: ["4.1"]
complexity: medium
---

- **Given** Story 4.1 emits events
- **Given** Slice 0a helper available
- **Refs:** FR-Arch-2
`), 0o644); err != nil {
		t.Fatal(err)
	}

	disableStreamTap = true
	t.Cleanup(func() { disableStreamTap = false })

	root := NewRootCmd(zap.NewNop())
	root.SetArgs([]string{"sprint", "infer-deps", "--epics", epics, "--no-log"})

	out := captureStdout(t, func() {
		if err := root.Execute(); err != nil {
			t.Fatalf("Execute: %v", err)
		}
	})

	if !strings.Contains(out, "Story 4.2") {
		t.Errorf("stdout missing Story 4.2 heading:\n%s", out)
	}
	if !strings.Contains(out, `depends_on: ["1.4", "4.1"]`) {
		t.Errorf("stdout missing merged depends_on:\n%s", out)
	}
	if !strings.Contains(out, "+ 1.4") {
		t.Errorf("stdout missing new-dep marker:\n%s", out)
	}
	if !strings.Contains(out, "agreement vs manual:") {
		t.Errorf("stdout missing agreement-rate footer:\n%s", out)
	}
}

func TestSprintInferDeps_Apply(t *testing.T) {
	dir := t.TempDir()
	epics := filepath.Join(dir, "epics.md")
	src := `## Epic 1: Slice 0a — Reference

### Story 1.4: Verify

## Epic 4: Slice 1 — identity

### Story 4.1: Aggregates

### Story 4.2: Canonical Service

---
story_id: "4.2"
depends_on: ["4.1"]
---

- **Given** Slice 0a helper available
- **Refs:** FR-Arch-2
`
	if err := os.WriteFile(epics, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	disableStreamTap = true
	t.Cleanup(func() { disableStreamTap = false })

	root := NewRootCmd(zap.NewNop())
	root.SetArgs([]string{"sprint", "infer-deps", "--epics", epics, "--apply", "--backup", "--no-log"})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	after, err := os.ReadFile(epics)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(after), `depends_on: ["1.4", "4.1"]`) {
		t.Errorf("apply didn't merge:\n%s", string(after))
	}

	back, err := os.ReadFile(epics + ".bak")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(back), `depends_on: ["4.1"]`) {
		t.Errorf("backup not the original:\n%s", string(back))
	}
}

func TestSprintInferDeps_JSONMode(t *testing.T) {
	dir := t.TempDir()
	epics := filepath.Join(dir, "epics.md")
	if err := os.WriteFile(epics, []byte(`## Epic 1: Slice 0a

### Story 1.1: A
### Story 1.2: B

- **Given** Story 1.1 ready
`), 0o644); err != nil {
		t.Fatal(err)
	}
	disableStreamTap = true
	t.Cleanup(func() { disableStreamTap = false })

	root := NewRootCmd(zap.NewNop())
	root.SetArgs([]string{"--json", "sprint", "infer-deps", "--epics", epics, "--no-log"})

	out := captureStdout(t, func() {
		if err := root.Execute(); err != nil {
			t.Fatalf("Execute: %v", err)
		}
	})

	if !strings.Contains(out, `"schema_version": "v1"`) {
		t.Errorf("expected v1 envelope: %s", out)
	}
	if !strings.Contains(out, `"command": "sprint infer-deps"`) {
		t.Errorf("expected command name in envelope: %s", out)
	}
	if !strings.Contains(out, `"dep_id": "1.1"`) {
		t.Errorf("expected inferred dep id in result: %s", out)
	}
}

// captureStdout redirects os.Stdout through a pipe while fn runs and
// returns whatever fn wrote to stdout. The audit-log stream-tap is
// disabled by the caller (disableStreamTap=true) so it doesn't interpose.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	done := make(chan struct{})
	var buf bytes.Buffer
	go func() {
		_, _ = io.Copy(&buf, r)
		close(done)
	}()

	fn()
	_ = w.Close()
	<-done
	os.Stdout = orig
	return buf.String()
}
