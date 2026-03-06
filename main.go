package main

import (
	"os"

	"go.uber.org/zap"

	"github.com/sosalejandro/bmad-story-runner-cli/cmd"
)

func main() {
	logger, err := zap.NewProduction()
	if err != nil {
		panic("failed to initialize logger: " + err.Error())
	}
	defer logger.Sync() //nolint:errcheck

	root := cmd.NewRootCmd(logger)
	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}
