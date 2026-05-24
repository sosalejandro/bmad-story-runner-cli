package cmd

import (
	"os"
	"strings"
)

// stateBackend is the resolved backend kind for cmds that historically took a
// positional progress-file path AND now must also accept the v6 SQLite store.
type stateBackend int

const (
	// backendSQLite means the v6 sqlite store is in play. The cmd should
	// open it via openV6DB (which honors --state / BMAD_STATE / cwd).
	backendSQLite stateBackend = iota
	// backendLegacyJSON means the operator handed us a v4 bmad-progress.json
	// path explicitly — fall through to the legacy NewJSONProgressStore code
	// path so back-compat workflows still run.
	backendLegacyJSON
)

// resolveStateBackend picks the backend for `bmad list` / `bmad add-concerns`
// (issue #71). Rule:
//
//  1. If no positional arg is supplied, use SQLite (the modern default).
//  2. If the arg ends in `.db` (case-insensitive), use SQLite and route the
//     path through --state by overwriting v6StatePathFlag — that way every
//     downstream call to resolveStatePath / openV6DB sees the operator
//     intent.
//  3. Otherwise treat the arg as a legacy JSON path (v4 back-compat).
//
// The returned `consumedArg` is true when the function captured the
// positional arg for its own purposes (case 2); the caller should NOT
// then forward it to the legacy JSON-store code.
func resolveStateBackend(arg string) (backend stateBackend, consumedArg bool) {
	if arg == "" {
		return backendSQLite, false
	}
	lower := strings.ToLower(arg)
	if strings.HasSuffix(lower, ".db") {
		// Operator handed us an explicit .db path — route it through the
		// --state flag so openV6DB picks it up. We deliberately overwrite
		// even if --state was already set; CLI positional args win
		// over implicit defaults but lose to nothing (matches the
		// precedence operators expect from other Unix tools).
		v6StatePathFlag = absPath(arg)
		return backendSQLite, true
	}
	if strings.HasSuffix(lower, ".json") {
		return backendLegacyJSON, false
	}
	// Unknown suffix: if the file exists and starts with the SQLite magic
	// bytes ("SQLite format 3"), trust that. Otherwise fall back to legacy
	// JSON (preserves pre-#71 behaviour for anyone calling with a path that
	// happens to lack an extension).
	if isSQLiteFile(arg) {
		v6StatePathFlag = absPath(arg)
		return backendSQLite, true
	}
	return backendLegacyJSON, false
}

// isSQLiteFile peeks the first 16 bytes for the standard SQLite header.
// Returns false on any read error — callers fall back to legacy JSON.
func isSQLiteFile(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	header := make([]byte, 16)
	n, err := f.Read(header)
	if err != nil || n < 16 {
		return false
	}
	return string(header) == "SQLite format 3\x00"
}
