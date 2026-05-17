package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/sosalejandro/bmad-story-runner-cli/infrastructure/state/sqlite"
)

// v6StatePathFlag is the persistent --state flag (overrides default cwd lookup).
var v6StatePathFlag string

// addV6PersistentFlags wires --state onto a v6 command (or its parent).
func addV6PersistentFlags(c *cobra.Command) {
	c.PersistentFlags().StringVar(&v6StatePathFlag, "state", "",
		"path to bmad-state.db (default: ./bmad-state.db)")
}

// resolveStatePath returns the absolute path of the state DB. Precedence:
//
//  1. --state flag (if set)
//  2. BMAD_STATE env var
//  3. ./bmad-state.db (resolved against cwd)
func resolveStatePath() (string, error) {
	if v6StatePathFlag != "" {
		return absPath(v6StatePathFlag), nil
	}
	if env := os.Getenv("BMAD_STATE"); env != "" {
		return absPath(env), nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("resolve cwd: %w", err)
	}
	return filepath.Join(cwd, "bmad-state.db"), nil
}

// openV6DB opens the resolved state DB and runs pending migrations.
func openV6DB(ctx context.Context) (*sqlite.DB, error) {
	path, err := resolveStatePath()
	if err != nil {
		return nil, err
	}
	db, err := sqlite.Open(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("open v6 state at %s: %w", path, err)
	}
	return db, nil
}

// openV6DBOrExit is the cobra-friendly variant; errors call os.Exit.
func openV6DBOrExit(ctx context.Context) *sqlite.DB {
	db, err := openV6DB(ctx)
	if err != nil {
		exitOnError(err)
	}
	return db
}
