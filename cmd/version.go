package cmd

import "fmt"

// Set via ldflags at build time:
//
//	go build -ldflags "\
//	  -X github.com/sosalejandro/bmad-story-runner-cli/cmd.Version=0.4.0 \
//	  -X github.com/sosalejandro/bmad-story-runner-cli/cmd.CommitSHA=$(git rev-parse --short HEAD) \
//	  -X github.com/sosalejandro/bmad-story-runner-cli/cmd.BuildDate=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
//
// These vars also drive cobra's built-in `--version` / `-v` flag — wired
// up by NewRootCmd via VersionString() below.
var (
	Version   = "dev"
	CommitSHA = "unknown"
	BuildDate = "unknown"
)

// VersionString returns the canonical multi-field version line used by
// the cobra root command's Version field. Keeping the formatting here
// (rather than inline in NewRootCmd) lets tests assert on the shape
// without booting the full command tree.
func VersionString() string {
	return fmt.Sprintf("%s (commit %s, built %s)", Version, CommitSHA, BuildDate)
}
