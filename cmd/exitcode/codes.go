// Package exitcode defines stable, AI-agent-consumable exit codes for the
// bmad CLI.
//
// These codes are part of the CLI's public contract: orchestrator agents
// (Claude Code, etc.) branch on them to decide whether to retry, escalate
// to a human, or log-and-move-on. Changing the integer value of an existing
// code is therefore a BREAKING change — bump the documented contract in
// README "For AI agents" and call it out in CHANGELOG.
//
// The taxonomy intentionally stays small (< 10 codes). Larger ranges invite
// drift: agents start hard-coding obscure values and the contract becomes
// impossible to evolve. If you find yourself wanting a new code, ask first
// whether the existing ones can carry the same information via the --json
// envelope's `result` body instead.
//
// Numbering convention:
//
//	0           success
//	1           generic / unclassified user error (legacy default — keep)
//	2           "no result" (e.g. `bmad next` with no eligible story; not an error)
//	10..19      USER input / argument problems
//	20..29      SYSTEM / I/O / environment problems
//	30..39      VALIDATION (input syntactically OK but semantically rejected)
//	40..49      NOT FOUND (resource referenced but absent)
//	50..59      CONFLICT (state-mutating call collides with prior state)
//
// The constants below are the only values that should appear in
// os.Exit(...) calls across cmd/. New callers should prefer the typed
// Error wrapper (Wrap/From) so a single os.Exit at the binary boundary
// can map errors → codes uniformly.
package exitcode

// Code is the integer exit code passed to os.Exit. We model it as a
// distinct type so callers cannot pass an arbitrary int by mistake.
type Code int

const (
	// Success — command completed as expected.
	Success Code = 0

	// UserError — generic user-facing failure. Use only when none of the
	// more specific codes below applies. Preserved at 1 to stay
	// backwards-compatible with shell idioms (`bmad foo || echo fail`).
	UserError Code = 1

	// NoResult — the command ran successfully but had nothing to return.
	// Canonical example: `bmad next` with no eligible story. Distinct
	// from UserError so orchestrators can treat it as a control-flow
	// signal rather than a failure.
	NoResult Code = 2

	// ArgsError — required argument missing, flag in wrong format, etc.
	// Reserved for shape-of-invocation problems detected before the
	// command does any real work.
	ArgsError Code = 10

	// SystemError — I/O, network, fs permission, or other environment
	// failure. The user's invocation was fine; the host wasn't.
	SystemError Code = 20

	// ValidationError — input parsed cleanly but failed a business rule
	// (e.g. status string not in the allowed enum, gate result outside
	// PASS/FAIL/CONCERNS).
	ValidationError Code = 30

	// NotFound — referenced resource (story, gate, env allocation,
	// dispatch key, etc.) does not exist in state.
	NotFound Code = 40

	// Conflict — a state-mutating call collides with prior state
	// (idempotency key already used with different payload, port already
	// allocated, etc.).
	Conflict Code = 50
)

// Int returns the integer value suitable for os.Exit.
func (c Code) Int() int { return int(c) }

// String returns a human-readable name for the code. Used in --help and
// `bmad doctor` output.
func (c Code) String() string {
	switch c {
	case Success:
		return "SUCCESS"
	case UserError:
		return "USER_ERROR"
	case NoResult:
		return "NO_RESULT"
	case ArgsError:
		return "ARGS_ERROR"
	case SystemError:
		return "SYSTEM_ERROR"
	case ValidationError:
		return "VALIDATION_ERROR"
	case NotFound:
		return "NOT_FOUND"
	case Conflict:
		return "CONFLICT"
	default:
		return "UNKNOWN"
	}
}

// All returns the documented exit code set in numerical order. Used by
// `bmad doctor` and `--help-json` to render the contract.
func All() []Code {
	return []Code{
		Success,
		UserError,
		NoResult,
		ArgsError,
		SystemError,
		ValidationError,
		NotFound,
		Conflict,
	}
}

// Describe returns a one-line explanation for the code, suitable for
// rendering in `bmad doctor` and machine-readable help.
func Describe(c Code) string {
	switch c {
	case Success:
		return "command completed successfully"
	case UserError:
		return "generic user-facing failure (unclassified)"
	case NoResult:
		return "command succeeded but produced no result (e.g. no eligible story)"
	case ArgsError:
		return "required argument missing or flag malformed"
	case SystemError:
		return "I/O, fs, or environment failure (host-level)"
	case ValidationError:
		return "input parsed but rejected by a business rule"
	case NotFound:
		return "referenced resource does not exist in state"
	case Conflict:
		return "state-mutating call collides with prior state"
	default:
		return "unknown exit code"
	}
}
