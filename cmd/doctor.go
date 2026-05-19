package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/sosalejandro/bmad-story-runner-cli/cmd/exitcode"
	"github.com/sosalejandro/bmad-story-runner-cli/infrastructure/state/sqlite"
)

// newDoctorCmd is the v6 "self-check" command. It probes the runtime
// environment an AI orchestrator depends on (bmad binary version, atlas
// binary, state DB, schema version, exit-code contract, idempotency
// surface) and prints a single report.
//
// Design goals (per issue #9 + AI-CLI best practices):
//
//   - One command, no arguments — agents can invoke it before any other
//     command to bail early if the environment is broken.
//   - Both human-readable (table-ish) and --json modes. The JSON shape
//     reuses the v1 envelope from cmd/jsonout.go so downstream tooling
//     parses it like any other --json result.
//   - Never panics. Every probe wraps its failure in a typed result row
//     so the agent sees ALL the broken pieces in one shot rather than
//     fixing them one-at-a-time.
//
// Exit semantics:
//
//   - All checks PASS → exit 0
//   - Any check FAIL → exit exitcode.SystemError (20)
//
// (We deliberately do NOT exit on WARN — those are advisory.)
func newDoctorCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Self-check: probe bmad/atlas binaries, state DB, schema version, exit-code contract",
		Long: `doctor verifies that an AI orchestrator's environment is healthy:

  - bmad binary version (and whether ldflags were stamped at build time)
  - atlas binary presence (used by sprint planning) + reported version
  - bmad-state.db existence at the resolved path + readable schema_version
  - Go runtime version of the running binary
  - documented stable exit codes (the public AI-agent contract)
  - documented idempotency surface (which state-mutating commands support
    idempotency keys today)

Exits 0 on all-pass, exitcode.SystemError (20) on any failed probe.
Run with --json for machine-readable output (envelope schema v1).`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, args []string) error {
			report := runDoctor(c.Context())
			if jsonOutput {
				if err := emitJSONStdout(commandPathSansRoot(c), map[string]any{}, report, nil); err != nil {
					return err
				}
			} else {
				printDoctorHuman(os.Stdout, report)
			}
			if !report.OK {
				// Surfacing as os.Exit (not returning a cobra error) keeps
				// stderr clean for agents — the human/json output already
				// explains the failure.
				os.Exit(exitcode.SystemError.Int())
			}
			return nil
		},
	}
	addV6PersistentFlags(cmd)
	return cmd
}

// doctorReport is the structured output of `bmad doctor`. Each Check is
// a single probe result; OK is true only when every check passed (i.e.
// no Status == "FAIL"). WARN-only reports are still OK=true.
type doctorReport struct {
	OK            bool             `json:"ok"`
	Checks        []doctorCheck    `json:"checks"`
	ExitCodes     []exitCodeEntry  `json:"exit_codes"`
	Idempotency   []idempotencyDoc `json:"idempotency_surface"`
	GeneratedAtNs int64            `json:"-"` // populated for tests; not in JSON
}

type doctorCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"` // PASS | WARN | FAIL
	Detail string `json:"detail"`
}

type exitCodeEntry struct {
	Code        int    `json:"code"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

type idempotencyDoc struct {
	Command string `json:"command"`
	Surface string `json:"surface"`
}

// runDoctor executes every probe and assembles the report. Pure-ish: the
// only side effects are reading from /proc, the filesystem, and child
// process invocations (atlas --version).
func runDoctor(ctx context.Context) doctorReport {
	r := doctorReport{
		GeneratedAtNs: time.Now().UnixNano(),
		ExitCodes:     buildExitCodeContract(),
		Idempotency:   buildIdempotencySurface(),
	}

	r.Checks = append(r.Checks, checkBmadVersion())
	r.Checks = append(r.Checks, checkGoRuntime())
	r.Checks = append(r.Checks, checkAtlasBinary(ctx))
	r.Checks = append(r.Checks, checkStateDB(ctx))

	r.OK = true
	for _, c := range r.Checks {
		if c.Status == "FAIL" {
			r.OK = false
			break
		}
	}
	return r
}

// ---------- individual probes ----------

func checkBmadVersion() doctorCheck {
	if Version == "dev" || Version == "" {
		return doctorCheck{
			Name:   "bmad version",
			Status: "WARN",
			Detail: fmt.Sprintf("Version=%q (binary not built with -ldflags -X cmd.Version=... — release builds set this)", Version),
		}
	}
	return doctorCheck{
		Name:   "bmad version",
		Status: "PASS",
		Detail: VersionString(),
	}
}

func checkGoRuntime() doctorCheck {
	return doctorCheck{
		Name:   "go runtime",
		Status: "PASS",
		Detail: fmt.Sprintf("%s (%s/%s)", runtime.Version(), runtime.GOOS, runtime.GOARCH),
	}
}

// checkAtlasBinary asks `atlas version` over PATH. atlas is required for
// `bmad sprint plan` (it provides the dependency-graph solver). Missing
// atlas is a FAIL because the orchestrator can't run a sprint without it.
func checkAtlasBinary(ctx context.Context) doctorCheck {
	path, err := exec.LookPath("atlas")
	if err != nil {
		return doctorCheck{
			Name:   "atlas binary",
			Status: "FAIL",
			Detail: "atlas not found on PATH; required for `bmad sprint plan` (install: go install github.com/sosalejandro/atlas/cmd/atlas@latest)",
		}
	}
	// Bounded probe — atlas version should print and exit promptly.
	cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	out, runErr := exec.CommandContext(cctx, "atlas", "version").CombinedOutput()
	if runErr != nil {
		// Fallback: --version flag form. Some older atlas builds use it.
		out, runErr = exec.CommandContext(cctx, "atlas", "--version").CombinedOutput()
	}
	if runErr != nil {
		return doctorCheck{
			Name:   "atlas binary",
			Status: "WARN",
			Detail: fmt.Sprintf("found at %s but version probe failed: %v", path, runErr),
		}
	}
	return doctorCheck{
		Name:   "atlas binary",
		Status: "PASS",
		Detail: fmt.Sprintf("%s — %s", path, firstLine(string(out))),
	}
}

// checkStateDB resolves the configured state DB path, verifies it exists,
// opens it (which runs pending migrations), and reads the applied
// schema_version rows.
func checkStateDB(ctx context.Context) doctorCheck {
	path, err := resolveStatePath()
	if err != nil {
		return doctorCheck{
			Name:   "bmad-state.db",
			Status: "FAIL",
			Detail: fmt.Sprintf("resolve state path: %v", err),
		}
	}

	if _, statErr := os.Stat(path); statErr != nil {
		if errors.Is(statErr, os.ErrNotExist) {
			return doctorCheck{
				Name:   "bmad-state.db",
				Status: "WARN",
				Detail: fmt.Sprintf("not yet created at %s — run `bmad init` to scaffold", path),
			}
		}
		return doctorCheck{
			Name:   "bmad-state.db",
			Status: "FAIL",
			Detail: fmt.Sprintf("stat %s: %v", path, statErr),
		}
	}

	db, err := sqlite.Open(ctx, path)
	if err != nil {
		return doctorCheck{
			Name:   "bmad-state.db",
			Status: "FAIL",
			Detail: fmt.Sprintf("open %s: %v", path, err),
		}
	}
	defer db.Close()

	versions, err := readSchemaVersions(ctx, db)
	if err != nil {
		return doctorCheck{
			Name:   "bmad-state.db",
			Status: "FAIL",
			Detail: fmt.Sprintf("read schema_version: %v", err),
		}
	}
	if len(versions) == 0 {
		return doctorCheck{
			Name:   "bmad-state.db",
			Status: "FAIL",
			Detail: fmt.Sprintf("%s exists but schema_version table empty — corrupt or pre-migration", path),
		}
	}
	return doctorCheck{
		Name:   "bmad-state.db",
		Status: "PASS",
		Detail: fmt.Sprintf("%s (schema versions: %s)", path, joinInts(versions)),
	}
}

// readSchemaVersions queries the applied schema versions through a tiny
// exported helper on the sqlite package. We keep the query inline (rather
// than threading a new method through the adapter) because it's a one-off
// for diagnostics — adding it to the domain port would be over-engineering.
func readSchemaVersions(ctx context.Context, db *sqlite.DB) ([]int, error) {
	return sqlite.AppliedSchemaVersions(ctx, db)
}

// ---------- contract builders ----------

func buildExitCodeContract() []exitCodeEntry {
	codes := exitcode.All()
	out := make([]exitCodeEntry, 0, len(codes))
	for _, c := range codes {
		out = append(out, exitCodeEntry{
			Code:        c.Int(),
			Name:        c.String(),
			Description: exitcode.Describe(c),
		})
	}
	return out
}

// buildIdempotencySurface documents the existing state-mutating surface
// that already supports idempotency keys. This is documentation-only:
// `bmad doctor` does not verify the keys are wired correctly. Adding a
// new entry here is the contract — agents read it to know which commands
// they can safely retry without a custom "already done?" check.
//
// Source of truth: cmd/dispatch.go (begin/record both take --key /
// --idempotency-key) and cmd/render.go (--idempotency-key threading).
func buildIdempotencySurface() []idempotencyDoc {
	return []idempotencyDoc{
		{
			Command: "bmad dispatch begin",
			Surface: "emits a UUID idempotency_key on the JSON envelope; record retries match on it",
		},
		{
			Command: "bmad dispatch record",
			Surface: "accepts --key (preferred) — re-recording the same key is a no-op against the same payload",
		},
		{
			Command: "bmad render",
			Surface: "accepts --idempotency-key — key is injected into the rendered prompt and echoed back by the agent",
		},
	}
}

// ---------- human-readable printer ----------

func printDoctorHuman(w *os.File, r doctorReport) {
	fmt.Fprintln(w, "bmad doctor — environment self-check")
	fmt.Fprintln(w)
	for _, c := range r.Checks {
		fmt.Fprintf(w, "  [%s] %s\n        %s\n", c.Status, c.Name, c.Detail)
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Exit-code contract (stable for AI agents):")
	for _, e := range r.ExitCodes {
		fmt.Fprintf(w, "  %3d  %-18s %s\n", e.Code, e.Name, e.Description)
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Idempotency surface (commands safe to retry with same --key):")
	for _, i := range r.Idempotency {
		fmt.Fprintf(w, "  - %s\n        %s\n", i.Command, i.Surface)
	}
	fmt.Fprintln(w)
	if r.OK {
		fmt.Fprintln(w, "OK — all checks passed.")
	} else {
		fmt.Fprintln(w, "FAIL — one or more checks failed (exit 20).")
	}
}

// ---------- tiny helpers ----------

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}

func joinInts(xs []int) string {
	parts := make([]string, len(xs))
	for i, x := range xs {
		parts[i] = fmt.Sprintf("%d", x)
	}
	return strings.Join(parts, ",")
}
