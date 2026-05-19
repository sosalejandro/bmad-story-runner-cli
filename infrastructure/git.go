package infrastructure

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// GitRunner is the narrow interface the compound `bmad story complete
// --commit` flow uses to talk to git. Production code uses ExecGitRunner;
// tests inject a mock that records the calls without spawning subprocesses.
//
// The interface is intentionally tiny — the compound workflow only needs
// three operations (dirty-check, stage one file, commit) — and every
// method returns the captured output so the test mock can assert on the
// arg vector verbatim.
type GitRunner interface {
	// Status returns `git status --porcelain` output. Empty string means
	// the working tree is clean (committed-equal-to-HEAD; no untracked
	// files visible to git).
	Status(ctx context.Context, repoDir string) (string, error)

	// Add stages the given path. Equivalent to `git add -- <path>`.
	Add(ctx context.Context, repoDir, path string) error

	// Commit creates a commit with the given message. Equivalent to
	// `git commit -m <msg>`. No-op via the runner when there is nothing
	// staged — callers must check Status() first.
	Commit(ctx context.Context, repoDir, message string) (sha string, err error)
}

// ExecGitRunner is the production GitRunner. It shells out to the local
// `git` binary. Stderr is folded into the returned error so the caller
// can surface git's own diagnostics (e.g. "not a git repository").
type ExecGitRunner struct{}

// Status implements GitRunner.
func (ExecGitRunner) Status(ctx context.Context, repoDir string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", repoDir, "status", "--porcelain")
	out, err := cmd.Output()
	if err != nil {
		return "", wrapGitErr("status", err)
	}
	return string(out), nil
}

// Add implements GitRunner.
func (ExecGitRunner) Add(ctx context.Context, repoDir, path string) error {
	cmd := exec.CommandContext(ctx, "git", "-C", repoDir, "add", "--", path)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git add %q: %w (%s)", path, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Commit implements GitRunner. Returns the new commit's short SHA on
// success. The `--quiet` flag keeps stdout silent for clean json-mode
// output; the SHA is read via a follow-up `git rev-parse HEAD`.
func (ExecGitRunner) Commit(ctx context.Context, repoDir, message string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", repoDir, "commit", "-m", message)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("git commit: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	head := exec.CommandContext(ctx, "git", "-C", repoDir, "rev-parse", "--short", "HEAD")
	out, err := head.Output()
	if err != nil {
		// Commit succeeded but rev-parse failed — return empty SHA so
		// the caller still records the action without aborting.
		return "", nil
	}
	return strings.TrimSpace(string(out)), nil
}

func wrapGitErr(op string, err error) error {
	if ee, ok := err.(*exec.ExitError); ok {
		return fmt.Errorf("git %s: %w (%s)", op, err, strings.TrimSpace(string(ee.Stderr)))
	}
	return fmt.Errorf("git %s: %w", op, err)
}

// IsCleanForStoryFile returns (clean, reason). A tree is "clean enough"
// for the compound workflow when every porcelain entry refers to the
// given story file. Any other modified/added/untracked path means we
// must NOT auto-commit — the operator probably has unrelated work that
// should land in its own commit.
//
// porcelain format: two-char status + space + path. Renames look like
// "R  old -> new" — we conservatively reject those (a rename is rarely
// a story-status-only edit anyway).
func IsCleanForStoryFile(porcelain, storyFilePath string) (bool, string) {
	// Porcelain v1 format per line: `XY PATH` where XY is exactly 2 chars
	// (status code for index + working tree), then 1 space, then PATH.
	// The leading char is often a space (e.g. " M foo.go" means "modified
	// in working tree, unchanged in index"), so DO NOT TrimSpace the whole
	// blob — that would strip the X char and shift the path slice.
	if strings.TrimSpace(porcelain) == "" {
		// Nothing changed at all — caller will treat this as a no-op
		// (the mark-story-file step is supposed to have just modified
		// the file; if porcelain is empty the patcher was idempotent
		// and there's nothing to commit).
		return true, ""
	}
	for _, line := range strings.Split(porcelain, "\n") {
		// Trim only trailing \r (from CRLF systems) and the trailing \n
		// the splitter doesn't strip on the last entry.
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			continue
		}
		if len(line) < 4 {
			continue
		}
		// Skip the two-char status + the separating space.
		path := line[3:]
		// Renames embed " -> " — bail out conservatively.
		if strings.Contains(path, " -> ") {
			return false, "rename detected: " + path
		}
		if path != storyFilePath {
			return false, "unrelated change: " + path
		}
	}
	return true, ""
}
