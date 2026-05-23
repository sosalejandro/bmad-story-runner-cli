// Package cmd — install subcommand.
//
// `bmad install` drops the embedded L3 agents + skills into a target
// `.claude/` directory so consumers get the L1+L3 stack from a single
// `go install` of the CLI, with no cross-repo file copy.
//
// # Conflict policy
//
// The default behavior is *safe-by-default*: if any file already exists
// at the target path with content that differs from what we'd write,
// the command refuses (exit code 50 — CONFLICT) and lists the diverged
// files on stderr. `--force` skips the comparison and overwrites
// everything; `--dry-run` only prints the plan and never touches disk.
//
// Identical content (same bytes) is *not* a conflict — re-running
// `bmad install` after a clean install is a no-op, so CI / setup scripts
// can call it idempotently without `--force`.
//
// # JSON envelope
//
// When --json is passed, the command emits an installResult struct
// describing what was written (or what would be written for --dry-run)
// and any per-file conflicts that blocked the run. The envelope's
// `warnings` slice carries non-fatal nudges (e.g. existing files that
// happened to be byte-identical and so were skipped silently).
package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sosalejandro/bmad-story-runner-cli/assets"
	"github.com/sosalejandro/bmad-story-runner-cli/cmd/exitcode"
)

// installAction tags each planned write so the JSON output + dry-run
// summary can describe what would happen at the per-file level.
type installAction string

const (
	actionWrite          installAction = "write"          // new file, no existing target
	actionOverwriteForce installAction = "overwrite"      // existed + differs, --force in effect
	actionSkipIdentical  installAction = "skip-identical" // existed + matches; nothing to do
	actionConflict       installAction = "conflict"       // existed + differs, no --force → would block
)

// installPlanEntry is one row of the plan. The plan is computed in full
// before any write happens so we can refuse atomically on conflict.
type installPlanEntry struct {
	// SourcePath is the path inside the embedded FS (e.g.
	// "agents/atdd-writer.md") that supplied the content.
	SourcePath string `json:"source_path"`
	// TargetPath is the absolute on-disk path that would be written.
	TargetPath string `json:"target_path"`
	// Action is what the installer would do for this entry.
	Action installAction `json:"action"`
}

// installResult is the --json result body.
type installResult struct {
	Target    string             `json:"target"`
	DryRun    bool               `json:"dry_run"`
	Force     bool               `json:"force"`
	Plan      []installPlanEntry `json:"plan"`
	Written   int                `json:"written"`
	Skipped   int                `json:"skipped"`
	Conflicts int                `json:"conflicts"`
}

// newInstallCmd wires the `bmad install` cobra command.
func newInstallCmd() *cobra.Command {
	var (
		target string
		force  bool
		dryRun bool
	)
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install embedded L3 agents + skills into a target .claude/ directory",
		Long: `Drops the embedded L3 agent definitions and skill bundles into the
target directory so a fresh project can consume the BMad L1+L3 stack
from a single 'go install' of this CLI — no cross-repo file copy
needed.

Default target is '.claude/' relative to the current working directory.
The agents land under '<target>/agents/' and skills land under
'<target>/skills/<skill-name>/'.

Conflict policy:
  - default: refuse (exit 50) if any file exists with different content,
    listing the diverged files on stderr. Identical bytes are skipped
    silently (so re-running is idempotent).
  - --force: overwrite everything regardless of existing content.
  - --dry-run: print the plan and exit without touching disk.

JSON output (--json) emits the full plan + per-action counters under
the schema v1 envelope.`,
		RunE: func(c *cobra.Command, args []string) error {
			absTarget, err := filepath.Abs(target)
			if err != nil {
				return fmt.Errorf("resolve target path: %w", err)
			}

			plan, err := buildInstallPlan(assets.Agents, assets.Skills, absTarget, force)
			if err != nil {
				return fmt.Errorf("build install plan: %w", err)
			}

			result := installResult{
				Target: absTarget,
				DryRun: dryRun,
				Force:  force,
				Plan:   plan,
			}
			conflicts := []string{}
			for _, p := range plan {
				switch p.Action {
				case actionConflict:
					conflicts = append(conflicts, p.TargetPath)
					result.Conflicts++
				case actionSkipIdentical:
					result.Skipped++
				}
			}

			// Conflict gate: refuse the whole run if any entry conflicts
			// and we don't have --force or --dry-run. Atomicity matters —
			// we don't want a partial install where some files landed and
			// some didn't.
			if len(conflicts) > 0 && !force && !dryRun {
				sort.Strings(conflicts)
				fmt.Fprintln(os.Stderr, "bmad install: refusing to overwrite existing files with differing content")
				for _, p := range conflicts {
					fmt.Fprintf(os.Stderr, "  conflict: %s\n", p)
				}
				fmt.Fprintln(os.Stderr, "use --force to overwrite, or --dry-run to preview")

				if jsonOutput {
					_ = emitJSONStdout(commandPathSansRoot(c), installArgsMap(target, force, dryRun), result, conflictWarnings(conflicts))
				}
				os.Exit(exitcode.Conflict.Int())
			}

			// Dry-run: just emit the plan, no I/O.
			if dryRun {
				if jsonOutput {
					return emitJSONStdout(commandPathSansRoot(c), installArgsMap(target, force, dryRun), result, nil)
				}
				printDryRunHuman(absTarget, plan)
				return nil
			}

			// Execute the plan.
			written, skipped, err := executeInstallPlan(plan, assets.Agents, assets.Skills)
			if err != nil {
				return fmt.Errorf("execute install plan: %w", err)
			}
			result.Written = written
			result.Skipped = skipped

			if jsonOutput {
				return emitJSONStdout(commandPathSansRoot(c), installArgsMap(target, force, dryRun), result, nil)
			}
			printExecuteHuman(absTarget, written, skipped, force)
			return nil
		},
	}

	cmd.Flags().StringVar(&target, "target", ".claude", "target directory to install agents/ and skills/ into")
	cmd.Flags().BoolVar(&force, "force", false, "overwrite existing files even if their content differs")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print what would be written without modifying anything")

	return cmd
}

// buildInstallPlan walks both embedded FSes and produces one plan entry per
// regular file. The plan classifies each entry so the executor can act
// without re-reading the target file. Splitting plan from execution gives
// us atomic conflict detection (we know the full set of conflicts before
// touching anything).
func buildInstallPlan(agents, skills fs.FS, absTarget string, force bool) ([]installPlanEntry, error) {
	plan := []installPlanEntry{}

	if err := appendFSToPlan(&plan, agents, absTarget, force); err != nil {
		return nil, err
	}
	if err := appendFSToPlan(&plan, skills, absTarget, force); err != nil {
		return nil, err
	}

	// Stable order — embed-walk order is already deterministic across
	// builds, but sorting by SourcePath makes diffs across runs trivially
	// comparable and gives tests a stable assertion target.
	sort.Slice(plan, func(i, j int) bool {
		return plan[i].SourcePath < plan[j].SourcePath
	})
	return plan, nil
}

// appendFSToPlan walks a single embedded FS and appends one entry per
// regular file. The embedded FS path becomes the target sub-path (so
// "agents/atdd-writer.md" lands at "<target>/agents/atdd-writer.md").
//
// Files at the *immediate root of a top-level directory* (e.g.
// `skills/README.md`) are skipped — those are bmad-cli's developer-facing
// documentation about the asset bundle and have no place in a consumer's
// .claude/ tree. Anything two-or-more levels deep
// (`skills/<skill>/SKILL.md`, `skills/<skill>/<helper>.md`, etc.) ships
// as-is because it is part of an actual skill bundle.
func appendFSToPlan(plan *[]installPlanEntry, src fs.FS, absTarget string, force bool) error {
	return fs.WalkDir(src, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		// Skip the embedded root marker "." itself (WalkDir yields it for
		// directories only, but be defensive).
		if path == "." {
			return nil
		}

		// Skip developer-facing files at depth 2 (e.g. `skills/README.md`).
		// The path has the form `<top>/<file>` for these — exactly two
		// segments — whereas a real shipped file is `<top>/<dir>/...`
		// (three or more segments) for skill bundles, or `<top>/<file>`
		// where `<top>` is `agents` (the agent files are the leaves).
		// To distinguish: filter only when the top-level segment is
		// "skills" AND there are exactly two segments. Agents stay
		// untouched because their bundle is a flat list of leaves.
		segments := strings.Split(filepath.ToSlash(path), "/")
		if len(segments) == 2 && segments[0] == "skills" {
			return nil
		}

		// Embedded paths always use forward slashes. Convert to the
		// host's path separator for the on-disk target so Windows
		// installs land in the right place.
		targetPath := filepath.Join(absTarget, filepath.FromSlash(path))

		action, err := classifyAction(src, path, targetPath, force)
		if err != nil {
			return err
		}

		*plan = append(*plan, installPlanEntry{
			SourcePath: path,
			TargetPath: targetPath,
			Action:     action,
		})
		return nil
	})
}

// classifyAction inspects the existing target file (if any) and decides
// which installAction applies. The decision is content-aware: byte-equal
// files never count as conflicts, so re-running install after a clean
// install is a no-op without --force.
func classifyAction(src fs.FS, srcPath, targetPath string, force bool) (installAction, error) {
	existing, err := os.ReadFile(targetPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return actionWrite, nil
		}
		return "", fmt.Errorf("read existing %s: %w", targetPath, err)
	}

	wanted, err := fs.ReadFile(src, srcPath)
	if err != nil {
		return "", fmt.Errorf("read embedded %s: %w", srcPath, err)
	}

	if bytes.Equal(existing, wanted) {
		return actionSkipIdentical, nil
	}
	if force {
		return actionOverwriteForce, nil
	}
	return actionConflict, nil
}

// executeInstallPlan applies a non-dry-run plan to disk. Skip-identical
// rows are no-ops; conflict rows MUST NOT appear here (the caller is
// responsible for refusing earlier when --force is unset). Returns
// (written, skipped) counts.
func executeInstallPlan(plan []installPlanEntry, agents, skills fs.FS) (written, skipped int, err error) {
	for _, p := range plan {
		switch p.Action {
		case actionSkipIdentical:
			skipped++
			continue
		case actionConflict:
			// Defensive — caller should have refused already. Surface
			// this as an internal-bug error if it ever fires.
			return written, skipped, fmt.Errorf("install plan contains a conflict at %s but executor was invoked anyway", p.TargetPath)
		case actionWrite, actionOverwriteForce:
			if err := writePlanEntry(p, agents, skills); err != nil {
				return written, skipped, err
			}
			written++
		default:
			return written, skipped, fmt.Errorf("unknown install action %q for %s", p.Action, p.TargetPath)
		}
	}
	return written, skipped, nil
}

// writePlanEntry materializes one plan row to disk. The intermediate
// directories are created with 0o755 (matching the rest of the CLI's
// mkdir calls) and files are written 0o644.
func writePlanEntry(p installPlanEntry, agents, skills fs.FS) error {
	src := pickEmbeddedFS(p.SourcePath, agents, skills)
	content, err := fs.ReadFile(src, p.SourcePath)
	if err != nil {
		return fmt.Errorf("read embedded %s: %w", p.SourcePath, err)
	}
	if err := os.MkdirAll(filepath.Dir(p.TargetPath), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(p.TargetPath), err)
	}
	if err := os.WriteFile(p.TargetPath, content, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", p.TargetPath, err)
	}
	return nil
}

// pickEmbeddedFS routes a source path back to the FS it came from. We
// know the top-level prefix because go:embed preserves directory names
// at the root of each embed.FS.
func pickEmbeddedFS(srcPath string, agents, skills fs.FS) fs.FS {
	if strings.HasPrefix(srcPath, "agents/") || srcPath == "agents" {
		return agents
	}
	return skills
}

// printDryRunHuman renders the plan in a per-action grouped form for
// humans. JSON consumers should use --json instead.
func printDryRunHuman(absTarget string, plan []installPlanEntry) {
	fmt.Printf("bmad install --dry-run (target: %s)\n", absTarget)
	fmt.Println("")
	groups := map[installAction][]string{}
	for _, p := range plan {
		groups[p.Action] = append(groups[p.Action], p.TargetPath)
	}
	order := []installAction{actionWrite, actionOverwriteForce, actionSkipIdentical, actionConflict}
	labels := map[installAction]string{
		actionWrite:          "would write (new):",
		actionOverwriteForce: "would overwrite (--force):",
		actionSkipIdentical:  "would skip (identical):",
		actionConflict:       "WOULD CONFLICT (existing differs):",
	}
	for _, a := range order {
		paths := groups[a]
		if len(paths) == 0 {
			continue
		}
		fmt.Println(labels[a])
		for _, p := range paths {
			fmt.Printf("  %s\n", p)
		}
		fmt.Println("")
	}
	fmt.Printf("total: %d entries\n", len(plan))
}

// printExecuteHuman renders the post-install summary for humans.
func printExecuteHuman(absTarget string, written, skipped int, force bool) {
	mode := "safe"
	if force {
		mode = "force"
	}
	fmt.Printf("bmad install: installed embedded agents + skills into %s (%s mode)\n", absTarget, mode)
	fmt.Printf("  written: %d files\n", written)
	if skipped > 0 {
		fmt.Printf("  skipped (already up-to-date): %d files\n", skipped)
	}
}

// installArgsMap is the deterministic args block emitted in the JSON
// envelope. Keeping this in one place avoids drift between RunE branches.
func installArgsMap(target string, force, dryRun bool) map[string]any {
	return map[string]any{
		"target":  target,
		"force":   force,
		"dry_run": dryRun,
	}
}

// conflictWarnings turns the sorted conflict paths into the warnings
// slice the JSON envelope expects. We surface them as warnings even
// though the exit code already signals CONFLICT so downstream tooling
// can grep the envelope without re-reading stderr.
func conflictWarnings(conflicts []string) []string {
	out := make([]string, 0, len(conflicts))
	for _, c := range conflicts {
		out = append(out, fmt.Sprintf("conflict: %s differs from embedded content", c))
	}
	return out
}
