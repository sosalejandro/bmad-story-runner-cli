package cmd

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	appstate "github.com/sosalejandro/bmad-story-runner-cli/application/state"
	"github.com/sosalejandro/bmad-story-runner-cli/infrastructure/state/sqlite"
)

// newInitCmd is the v6 init: scaffold bmad-state.db + seed default config rows.
// The v4 init behavior (scanning docs and creating bmad-progress.json) is
// preserved under `bmad migrate --from <progress.json>`.
func newInitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init [<docs-folder>]",
		Short: "Scaffold bmad-state.db (sqlite) + seed default config",
		Long: `Creates the v6 sqlite state store and seeds default runtime config
per spec §7. Idempotent: existing config values are preserved on re-init.

The optional docs-folder is recorded under the docs_folder config key
(always overwritten on init — it is the user-asserted intent).
If omitted, defaults to the current working directory.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			ctx := context.Background()

			db, err := openV6DB(ctx)
			if err != nil {
				return err
			}
			defer db.Close()

			docsFolder := "."
			if len(args) == 1 {
				docsFolder = absPath(args[0])
			} else {
				docsFolder = absPath(".")
			}

			cfg := sqlite.NewConfigStore(db)
			uc := appstate.NewInitUseCase(cfg)
			res, err := uc.Execute(ctx, docsFolder)
			if err != nil {
				return err
			}

			fmt.Printf("Initialized v6 state at %s\n", db.Path())
			fmt.Printf("  docs_folder: %s\n", res.DocsFolder)
			fmt.Printf("  seeded defaults: %d keys\n", len(res.SeededKeys))
			if n := len(res.SkippedKeys); n > 0 {
				fmt.Printf("  preserved existing values: %d keys\n", n)
			}
			return nil
		},
	}
	addV6PersistentFlags(cmd)
	return cmd
}
