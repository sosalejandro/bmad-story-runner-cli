package cmd

import (
	"fmt"
	"runtime/debug"
)

// Set via ldflags at build time:
//
//	go build -ldflags "\
//	  -X github.com/sosalejandro/bmad-story-runner-cli/cmd.Version=0.4.0 \
//	  -X github.com/sosalejandro/bmad-story-runner-cli/cmd.CommitSHA=$(git rev-parse --short HEAD) \
//	  -X github.com/sosalejandro/bmad-story-runner-cli/cmd.BuildDate=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
//
// These vars also drive cobra's built-in `--version` / `-v` flag — wired
// up by NewRootCmd via VersionString() below.
//
// When ldflags are not provided (e.g. `go install module@vX.Y.Z`), the
// runtime/debug.ReadBuildInfo() fallback in VersionString() reads the
// module version and VCS metadata that the Go toolchain embeds in every
// binary it produces. This means `go install`-based installs now report
// the actual installed version instead of the default ldflags placeholders.
var (
	Version   = "v0.5.2"
	CommitSHA = "b35491f"
	BuildDate = "2026-05-24T01:38:17Z"
)

// defaults captures the package-level zero state so VersionString can tell
// whether a value came from ldflags (overridden) or is still the placeholder
// that should be replaced with BuildInfo-derived data.
const (
	defaultVersion   = "dev"
	defaultCommitSHA = "unknown"
	defaultBuildDate = "unknown"
)

// VersionString returns the canonical multi-field version line used by
// the cobra root command's Version field. Keeping the formatting here
// (rather than inline in NewRootCmd) lets tests assert on the shape
// without booting the full command tree.
//
// Resolution order:
//  1. Explicit ldflags overrides (any of Version/CommitSHA/BuildDate set to a
//     non-default value) take precedence — preserves the release-please
//     workflow's existing -X stamping behaviour.
//  2. runtime/debug.ReadBuildInfo() — extracts the module version
//     (`bi.Main.Version`) plus the `vcs.revision` and `vcs.time` settings
//     that the Go toolchain embeds when building from a VCS checkout or
//     `go install module@version`.
//  3. The hard-coded "dev" / "unknown" defaults — only reached when
//     ReadBuildInfo returns ok=false (a truly anonymous build, e.g. a
//     stripped binary built with -buildvcs=false).
func VersionString() string {
	version, commit, date := resolveVersionFields()
	return fmt.Sprintf("%s (commit %s, built %s)", version, commit, date)
}

// resolveVersionFields applies the precedence rules documented on
// VersionString. Split out so tests can assert on the resolved tuple
// without parsing the formatted string.
func resolveVersionFields() (version, commit, date string) {
	version, commit, date = Version, CommitSHA, BuildDate

	// If any field is still at its default, try to fill it from BuildInfo.
	// We intentionally fill fields independently rather than all-or-nothing
	// so a partial ldflags stamp (e.g. only -X cmd.Version=...) still gets
	// VCS metadata from BuildInfo for the other fields.
	if version != defaultVersion && commit != defaultCommitSHA && date != defaultBuildDate {
		return normalizeVersion(version), commit, date
	}

	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return normalizeVersion(version), commit, date
	}

	if version == defaultVersion && bi.Main.Version != "" {
		version = bi.Main.Version
	}

	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			if commit == defaultCommitSHA && s.Value != "" {
				commit = shortSHA(s.Value)
			}
		case "vcs.time":
			if date == defaultBuildDate && s.Value != "" {
				date = s.Value
			}
		}
	}

	return normalizeVersion(version), commit, date
}

// normalizeVersion maps Go's literal "(devel)" sentinel — produced by
// `go install .` or `go run` against a local working tree — to the more
// user-friendly "dev" string. Surfacing "(devel)" verbatim leaks an
// implementation detail of the Go toolchain that isn't useful to end users.
func normalizeVersion(v string) string {
	if v == "(devel)" {
		return "dev"
	}
	return v
}

// shortSHA returns the first 7 chars of a full git SHA, matching the
// `git rev-parse --short` convention used by the ldflags release stamp.
// Shorter inputs are returned unchanged so callers don't have to guard
// against unexpected VCS providers.
func shortSHA(full string) string {
	const shortLen = 7
	if len(full) <= shortLen {
		return full
	}
	return full[:shortLen]
}
