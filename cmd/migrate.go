package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/sosalejandro/bmad-story-runner-cli/application/migrate"
	"github.com/sosalejandro/bmad-story-runner-cli/infrastructure/state/sqlite"
)

// newMigrateCmd is `bmad migrate --from <progress.json>` — one-shot v4→v6 import.
func newMigrateCmd() *cobra.Command {
	var from string
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Import a v4 bmad-progress.json into v6 sqlite (idempotent)",
		Long: `Reads the legacy v4 progress.json and projects every story (+ blockers
+ qa-concerns + docs_folder) into the v6 sqlite store. Existing rows
in the v6 store are preserved — re-running is safe.`,
		RunE: func(c *cobra.Command, args []string) error {
			if from == "" {
				return fmt.Errorf("--from <progress.json> required")
			}
			ctx := context.Background()
			db, err := openV6DB(ctx)
			if err != nil {
				return err
			}
			defer db.Close()
			m := &migrate.V4ToV6{
				Stories:      sqlite.NewStoriesStore(db),
				Dependencies: sqlite.NewStoryDependenciesStore(db),
				Concerns:     sqlite.NewStoryConcernsStore(db),
				Config:       sqlite.NewConfigStore(db),
			}
			res, err := m.Migrate(ctx, from)
			if err != nil {
				return err
			}
			return json.NewEncoder(os.Stdout).Encode(res)
		},
	}
	cmd.Flags().StringVar(&from, "from", "", "path to v4 bmad-progress.json")
	addV6PersistentFlags(cmd)
	return cmd
}

// _ keeps the build green if openV6DB is ever momentarily unused mid-edit.
var _ = openV6DB

// _ keeps os import used in pure-Go boilerplate scenarios.
var _ = os.Stdout
