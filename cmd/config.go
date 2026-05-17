package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/sosalejandro/bmad-story-runner-cli/domain/state"
	"github.com/sosalejandro/bmad-story-runner-cli/infrastructure/state/sqlite"
)

// newConfigCmd implements `bmad config <key> [<value>]`:
//
//   - no args: list all config rows
//   - one arg (key): print value, exit 1 if unset
//   - two args (key, value): upsert
func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config [<key>] [<value>]",
		Short: "Get or set runtime config (sqlite-backed)",
		Long: `Three forms:
  bmad config                  # list all keys (tabular)
  bmad config <key>            # print value for key
  bmad config <key> <value>    # upsert key=value`,
		Args: cobra.MaximumNArgs(2),
		RunE: func(c *cobra.Command, args []string) error {
			ctx := context.Background()
			db, err := openV6DB(ctx)
			if err != nil {
				return err
			}
			defer db.Close()
			cfg := sqlite.NewConfigStore(db)

			switch len(args) {
			case 0:
				return listAllConfig(ctx, cfg)
			case 1:
				return getOneConfig(ctx, cfg, args[0])
			case 2:
				return setOneConfig(ctx, cfg, args[0], args[1])
			}
			return nil
		},
	}
	addV6PersistentFlags(cmd)
	return cmd
}

func listAllConfig(ctx context.Context, cfg state.Config) error {
	entries, err := cfg.All(ctx)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		fmt.Println("(no config rows — run `bmad init` to seed defaults)")
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "KEY\tVALUE\tUPDATED")
	for _, e := range entries {
		fmt.Fprintf(w, "%s\t%s\t%s\n", e.Key, e.Value, e.UpdatedAt.Format("2006-01-02 15:04:05"))
	}
	return w.Flush()
}

func getOneConfig(ctx context.Context, cfg state.Config, key string) error {
	v, err := cfg.Get(ctx, key)
	if errors.Is(err, state.ErrNotFound) {
		fmt.Fprintf(os.Stderr, "config: key %q not set\n", key)
		os.Exit(1)
	}
	if err != nil {
		return err
	}
	fmt.Println(v)
	return nil
}

func setOneConfig(ctx context.Context, cfg state.Config, key, value string) error {
	if err := cfg.Set(ctx, key, value); err != nil {
		return err
	}
	fmt.Printf("%s = %s\n", key, value)
	return nil
}
