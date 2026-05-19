package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// jsonOutput is the top-level toggle wired at the root cobra command via
// `--json`. When true, every command that honors the flag emits its result
// as a stable v1 envelope (see jsonEnvelope) instead of the human-readable
// table / sentence form. When false (the default), commands keep their
// existing stdout exactly as before — the flag is purely additive so
// existing scripts that grep stdout don't break.
//
// We use a package-level var (rather than threading through every cobra
// RunE) so the legacy `Run` (non-RunE) commands in this package can read
// it without changing their signatures. Mutation happens once during root
// flag parsing; subcommands only read.
var jsonOutput bool

// schemaVersion is the version tag emitted in every JSON envelope. Bump
// (and ADD a migration note here) only on a breaking change — adding new
// optional fields to .result does NOT require a bump.
const schemaVersion = "v1"

// jsonEnvelope is the wire-format wrapper for every --json response. It's
// declared as a struct (not a map) so the field order in marshalled output
// is stable across runs — downstream JQ queries and golden tests rely on
// that ordering being deterministic.
type jsonEnvelope struct {
	SchemaVersion string         `json:"schema_version"`
	Command       string         `json:"command"`
	Args          map[string]any `json:"args"`
	Result        any            `json:"result"`
	Warnings      []string       `json:"warnings"`
	GeneratedAt   string         `json:"generated_at"`
}

// emitJSON renders the envelope to stdout. Use from a cobra command's
// RunE/Run body after `if jsonOutput`. Returns the marshal/write error so
// callers can propagate it (RunE) or wrap exit-on-error (Run).
//
// commandName: dotted form e.g. "story status" — matches what a user
// would type after `bmad`. Building it via cmd.CommandPath() minus the
// root name keeps it accurate even when a command is renamed.
//
// args: a map of the flags/positionals that influenced this result, so an
// orchestrator can correlate the envelope back to the invocation. Pass
// nil when there are no meaningful inputs.
//
// result: the per-command result body. Must marshal cleanly to JSON;
// callers should define a typed struct for stability rather than use raw
// map[string]any unless the shape is genuinely dynamic.
//
// warnings: non-fatal nudges (e.g. reaped stale claims, deprecated flag
// usage). Always emit a non-nil slice (even when empty) so consumers can
// do `len(.warnings)` without nil-checks.
func emitJSON(out io.Writer, commandName string, args map[string]any, result any, warnings []string) error {
	if warnings == nil {
		warnings = []string{}
	}
	if args == nil {
		args = map[string]any{}
	}
	env := jsonEnvelope{
		SchemaVersion: schemaVersion,
		Command:       commandName,
		Args:          args,
		Result:        result,
		Warnings:      warnings,
		GeneratedAt:   time.Now().UTC().Format(time.RFC3339),
	}
	b, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		return fmt.Errorf("emit json: %w", err)
	}
	if _, err := fmt.Fprintln(out, string(b)); err != nil {
		return fmt.Errorf("emit json: write: %w", err)
	}
	return nil
}

// emitJSONStdout is the common case — write to os.Stdout. Kept as a thin
// wrapper so tests can swap the writer via emitJSON directly.
func emitJSONStdout(commandName string, args map[string]any, result any, warnings []string) error {
	return emitJSON(os.Stdout, commandName, args, result, warnings)
}

// commandPathSansRoot returns the cobra CommandPath() with the leading
// root name stripped. Result is "story status", "sprint plan", etc — the
// shape downstream JQ queries and audit dashboards will key off.
func commandPathSansRoot(c *cobra.Command) string {
	full := c.CommandPath()
	if i := strings.Index(full, " "); i >= 0 {
		return full[i+1:]
	}
	return full
}
