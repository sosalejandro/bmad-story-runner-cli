package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/sosalejandro/bmad-story-runner-cli/assets"
	"github.com/sosalejandro/bmad-story-runner-cli/cmd/exitcode"
)

// expectedAgentBaseNames is the canonical L3 agent set the install
// command must ship. Adding a new agent file to the assets/agents tree
// requires updating this list — that's intentional: every shipped agent
// is a contract with consumers and must be acknowledged here.
var expectedAgentBaseNames = []string{
	"atdd-writer.md",
	"code-reviewer.md",
	"story-hydrator.md",
	"tdd-implementer.md",
	"test-automate.md",
	"test-reviewer.md",
}

// expectedSkillDirNames is the canonical skill set the install command
// must ship. Same contract rationale as expectedAgentBaseNames.
var expectedSkillDirNames = []string{
	"bmad-v6-orchestrator",
	"context-propagation",
	"docker-up",
	"healthcheck",
	"port-pool",
	"sprint-planning",
	"story-checkpoint",
	"sweeper",
}

// TestInstallPlan_CleanTarget confirms the plan against an empty target
// produces actionWrite for every expected entry and nothing else.
func TestInstallPlan_CleanTarget(t *testing.T) {
	t.Parallel()
	target := t.TempDir()

	plan, err := buildInstallPlan(assets.Agents, assets.Skills, target, false)
	if err != nil {
		t.Fatalf("buildInstallPlan: %v", err)
	}

	gotByName := map[string]installPlanEntry{}
	for _, p := range plan {
		gotByName[p.SourcePath] = p
	}

	for _, base := range expectedAgentBaseNames {
		src := "agents/" + base
		entry, ok := gotByName[src]
		if !ok {
			t.Errorf("plan missing agent %s", src)
			continue
		}
		if entry.Action != actionWrite {
			t.Errorf("agent %s action = %s, want %s", src, entry.Action, actionWrite)
		}
		wantTarget := filepath.Join(target, "agents", base)
		if entry.TargetPath != wantTarget {
			t.Errorf("agent %s target = %s, want %s", src, entry.TargetPath, wantTarget)
		}
	}

	for _, skillName := range expectedSkillDirNames {
		src := "skills/" + skillName + "/SKILL.md"
		entry, ok := gotByName[src]
		if !ok {
			t.Errorf("plan missing skill SKILL.md %s", src)
			continue
		}
		if entry.Action != actionWrite {
			t.Errorf("skill %s action = %s, want %s", src, entry.Action, actionWrite)
		}
	}

	// Plan must not include `skills/README.md` (developer-facing file).
	if _, has := gotByName["skills/README.md"]; has {
		t.Errorf("plan unexpectedly includes skills/README.md — should be filtered out")
	}
}

// TestInstallPlan_IdenticalSkip confirms a re-run after a clean install
// yields actionSkipIdentical for every entry (idempotency guarantee).
func TestInstallPlan_IdenticalSkip(t *testing.T) {
	t.Parallel()
	target := t.TempDir()

	plan, err := buildInstallPlan(assets.Agents, assets.Skills, target, false)
	if err != nil {
		t.Fatalf("buildInstallPlan (first): %v", err)
	}
	if _, _, err := executeInstallPlan(plan, assets.Agents, assets.Skills); err != nil {
		t.Fatalf("executeInstallPlan: %v", err)
	}

	plan2, err := buildInstallPlan(assets.Agents, assets.Skills, target, false)
	if err != nil {
		t.Fatalf("buildInstallPlan (second): %v", err)
	}
	for _, p := range plan2 {
		if p.Action != actionSkipIdentical {
			t.Errorf("second-run plan entry %s action = %s, want %s",
				p.SourcePath, p.Action, actionSkipIdentical)
		}
	}
}

// TestInstallPlan_DivergedFile_NoForce confirms a target file that
// differs from the embedded content classifies as actionConflict and
// the executor refuses to run.
func TestInstallPlan_DivergedFile_NoForce(t *testing.T) {
	t.Parallel()
	target := t.TempDir()

	// Pre-seed a divergent file at one expected target path.
	divergedPath := filepath.Join(target, "agents", "atdd-writer.md")
	if err := os.MkdirAll(filepath.Dir(divergedPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(divergedPath, []byte("STALE CONTENT - this should trigger a conflict"), 0o644); err != nil {
		t.Fatalf("write divergent: %v", err)
	}

	plan, err := buildInstallPlan(assets.Agents, assets.Skills, target, false)
	if err != nil {
		t.Fatalf("buildInstallPlan: %v", err)
	}

	var conflictEntry *installPlanEntry
	for i, p := range plan {
		if p.TargetPath == divergedPath {
			conflictEntry = &plan[i]
			break
		}
	}
	if conflictEntry == nil {
		t.Fatalf("plan missing entry for diverged path %s", divergedPath)
	}
	if conflictEntry.Action != actionConflict {
		t.Errorf("diverged entry action = %s, want %s", conflictEntry.Action, actionConflict)
	}

	// Executor must refuse the conflict row.
	if _, _, err := executeInstallPlan(plan, assets.Agents, assets.Skills); err == nil {
		t.Errorf("executeInstallPlan with conflict succeeded, want error")
	}
}

// TestInstallPlan_DivergedFile_Force confirms --force converts conflict
// rows to actionOverwriteForce and the executor writes through.
func TestInstallPlan_DivergedFile_Force(t *testing.T) {
	t.Parallel()
	target := t.TempDir()

	divergedPath := filepath.Join(target, "agents", "atdd-writer.md")
	if err := os.MkdirAll(filepath.Dir(divergedPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(divergedPath, []byte("STALE CONTENT"), 0o644); err != nil {
		t.Fatalf("write divergent: %v", err)
	}

	plan, err := buildInstallPlan(assets.Agents, assets.Skills, target, true)
	if err != nil {
		t.Fatalf("buildInstallPlan: %v", err)
	}

	var entry *installPlanEntry
	for i, p := range plan {
		if p.TargetPath == divergedPath {
			entry = &plan[i]
			break
		}
	}
	if entry == nil {
		t.Fatalf("plan missing entry for diverged path %s", divergedPath)
	}
	if entry.Action != actionOverwriteForce {
		t.Errorf("forced entry action = %s, want %s", entry.Action, actionOverwriteForce)
	}

	if _, _, err := executeInstallPlan(plan, assets.Agents, assets.Skills); err != nil {
		t.Fatalf("executeInstallPlan with --force: %v", err)
	}

	// After execute, the file content should now match the embedded source.
	wanted, err := assets.Agents.ReadFile("agents/atdd-writer.md")
	if err != nil {
		t.Fatalf("read embedded: %v", err)
	}
	got, err := os.ReadFile(divergedPath)
	if err != nil {
		t.Fatalf("read post-write: %v", err)
	}
	if !bytes.Equal(got, wanted) {
		t.Errorf("post-write content mismatch — --force did not overwrite")
	}
}

// TestInstallCommand_DryRun runs the cobra command in --dry-run mode
// against a temp target and verifies it does not touch disk.
//
// Not parallel — the runInstallCmd helper mutates the package-level
// jsonOutput global to test --json paths, so command-level tests must
// serialize among themselves.
func TestInstallCommand_DryRun(t *testing.T) {
	target := t.TempDir()

	out, err := runInstallCmd(t, "--target", target, "--dry-run")
	if err != nil {
		t.Fatalf("install --dry-run: %v\nout=%s", err, out)
	}
	if !strings.Contains(out, "would write") {
		t.Errorf("dry-run output missing 'would write': %s", out)
	}

	// Target must be empty (we created the tempdir, the dry-run must
	// not have added anything inside it).
	entries, err := os.ReadDir(target)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	if len(entries) != 0 {
		names := []string{}
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("dry-run created files: %v", names)
	}
}

// TestInstallCommand_CleanWrite runs the cobra command without flags
// against a clean tempdir and verifies the expected file tree appears.
//
// Serialized for the same jsonOutput global rationale as TestInstallCommand_DryRun.
func TestInstallCommand_CleanWrite(t *testing.T) {
	target := t.TempDir()

	if _, err := runInstallCmd(t, "--target", target); err != nil {
		t.Fatalf("install: %v", err)
	}

	// Every expected agent must be present.
	for _, base := range expectedAgentBaseNames {
		p := filepath.Join(target, "agents", base)
		if _, err := os.Stat(p); err != nil {
			t.Errorf("agent file missing: %s (%v)", p, err)
		}
	}
	// Every expected skill must have a SKILL.md.
	for _, name := range expectedSkillDirNames {
		p := filepath.Join(target, "skills", name, "SKILL.md")
		if _, err := os.Stat(p); err != nil {
			t.Errorf("skill SKILL.md missing: %s (%v)", p, err)
		}
	}
	// README.md must NOT be at skills root.
	readmePath := filepath.Join(target, "skills", "README.md")
	if _, err := os.Stat(readmePath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("skills/README.md present but should be filtered: %v", err)
	}
}

// TestInstallCommand_ConflictExitCode runs the binary as a subprocess
// (so os.Exit doesn't kill the test runner) and asserts the conflict
// exit code = 50 = exitcode.Conflict.
//
// We use a subprocess re-exec idiom rather than mocking os.Exit because
// the production code path is os.Exit(exitcode.Conflict.Int()), and
// asserting the actual binary semantics is the highest-confidence test.
func TestInstallCommand_ConflictExitCode(t *testing.T) {
	if os.Getenv("BMAD_INSTALL_CONFLICT_SUBPROC") == "1" {
		// Subprocess path: build the cobra command and run it.
		// runInstallCmd would catch the os.Exit; we want the real exit.
		runInstallCmdForReal()
		return
	}

	target := t.TempDir()
	if err := os.MkdirAll(filepath.Join(target, "agents"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(target, "agents", "atdd-writer.md"),
		[]byte("STALE"),
		0o644,
	); err != nil {
		t.Fatalf("seed: %v", err)
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestInstallCommand_ConflictExitCode")
	cmd.Env = append(os.Environ(),
		"BMAD_INSTALL_CONFLICT_SUBPROC=1",
		"BMAD_TEST_INSTALL_TARGET="+target,
	)
	out, err := cmd.CombinedOutput()
	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		t.Fatalf("expected non-zero exit from subprocess, got err=%v out=%s", err, out)
	}
	if ee.ExitCode() != exitcode.Conflict.Int() {
		t.Errorf("conflict exit code = %d, want %d (out=%s)", ee.ExitCode(), exitcode.Conflict.Int(), out)
	}
}

// runInstallCmdForReal is the subprocess entrypoint for the conflict
// exit-code test. It invokes the install command with the target env
// var so we get the real os.Exit semantics.
func runInstallCmdForReal() {
	target := os.Getenv("BMAD_TEST_INSTALL_TARGET")
	root := &cobra.Command{Use: "bmad"}
	root.PersistentFlags().BoolVar(&jsonOutput, "json", false, "")
	root.AddCommand(newInstallCmd())
	root.SetArgs([]string{"install", "--target", target})
	_ = root.Execute()
}

// TestInstallCommand_JSON exercises the --json envelope.
//
// Serialized for the same jsonOutput global rationale as the other
// cobra-driving tests.
func TestInstallCommand_JSON(t *testing.T) {
	target := t.TempDir()

	out, err := runInstallCmd(t, "--target", target, "--dry-run", "--json")
	if err != nil {
		t.Fatalf("install --json: %v\n%s", err, out)
	}

	var env struct {
		SchemaVersion string         `json:"schema_version"`
		Command       string         `json:"command"`
		Result        installResult  `json:"result"`
		Warnings      []string       `json:"warnings"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("unmarshal envelope: %v\nout=%s", err, out)
	}
	if env.SchemaVersion != "v1" {
		t.Errorf("schema_version = %q, want v1", env.SchemaVersion)
	}
	if env.Command != "install" {
		t.Errorf("command = %q, want install", env.Command)
	}
	if !env.Result.DryRun {
		t.Errorf("result.dry_run = false, want true")
	}
	if len(env.Result.Plan) != len(expectedAgentBaseNames)+len(expectedSkillDirNames) {
		t.Errorf("plan length = %d, want %d", len(env.Result.Plan),
			len(expectedAgentBaseNames)+len(expectedSkillDirNames))
	}
}

// runInstallCmd executes the install cobra command in-process with the
// provided args, capturing stdout. Returns the stdout content.
//
// We attach the command to a fresh cobra root so it doesn't inherit
// global state from other tests; the --json flag has to live on root
// since that's where the production wiring puts it.
func runInstallCmd(t *testing.T, args ...string) (string, error) {
	t.Helper()

	origStdout := os.Stdout
	origJSON := jsonOutput
	t.Cleanup(func() {
		os.Stdout = origStdout
		jsonOutput = origJSON
	})

	r, w, err := os.Pipe()
	if err != nil {
		return "", err
	}
	os.Stdout = w

	root := &cobra.Command{Use: "bmad"}
	root.PersistentFlags().BoolVar(&jsonOutput, "json", false, "")
	root.AddCommand(newInstallCmd())

	root.SetArgs(append([]string{"install"}, args...))
	execErr := root.Execute()

	_ = w.Close()
	buf, _ := io.ReadAll(r)
	out := string(buf)

	// Reset for downstream tests in this process.
	jsonOutput = origJSON

	// Sort plan-list output to give callers a stable substring to grep.
	_ = sort.StringSlice([]string{out})
	return out, execErr
}
