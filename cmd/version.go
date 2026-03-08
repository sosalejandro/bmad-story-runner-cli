package cmd

// Set via ldflags at build time:
//
//	go build -ldflags "-X github.com/sosalejandro/bmad-story-runner-cli/cmd.Version=0.4.0
//	  -X github.com/sosalejandro/bmad-story-runner-cli/cmd.CommitSHA=$(git rev-parse --short HEAD)"
var (
	Version   = "dev"
	CommitSHA = "unknown"
)
