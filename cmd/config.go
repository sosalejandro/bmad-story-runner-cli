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

// configFootgunKeys are the stray keys the pre-#16 ambiguous-positional CLI
// could accidentally write. We surface a one-time warning when the state DB
// holds any of them — making the cleanup discoverable without forcing a
// destructive migration. See issue #16.
var configFootgunKeys = []string{"get", "set"}

// newConfigCmd implements `bmad config <verb> ...`:
//
//   - `bmad config`                — list all rows
//   - `bmad config get <key>`      — read-only; never writes
//   - `bmad config set <key> <v>`  — write-only; requires both args
//   - `bmad config audit`          — surfaces any orphan keys from the
//                                    old ambiguous-positional footgun
//
// Pre-#16 forms (`bmad config foo bar` writing foo=bar; `bmad config foo`
// reading foo) accidentally let the L1 orchestrator create a stray "get"
// key — see issue #16. We now refuse positional misuse and route through
// explicit verbs.
func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config <verb>",
		Short: "Runtime config (sqlite-backed) — list / get / set / audit",
		Long: `Subcommands:
  bmad config              # list all keys
  bmad config get <key>    # print value (read-only; never writes)
  bmad config set <k> <v>  # upsert key=value (write-only; requires both args)
  bmad config audit        # report orphan keys from the pre-#16 footgun

The previous "bmad config <key> [<value>]" positional form is removed —
it let the L1 accidentally write a stray "get" key by mis-ordering args
(issue #16). Use the explicit verbs.`,
		// No Args: cobra.MaximumNArgs(...) at the parent — bare `bmad config`
		// listing is the only positional form, handled by RunE below; all
		// other paths go through the verb subcommands.
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, args []string) error {
			ctx := context.Background()
			db, err := openV6DB(ctx)
			if err != nil {
				return err
			}
			defer db.Close()
			cfg := sqlite.NewConfigStore(db)
			warnConfigFootguns(ctx, cfg)
			return listAllConfig(ctx, cfg)
		},
	}
	cmd.AddCommand(
		newConfigGetCmd(),
		newConfigSetCmd(),
		newConfigAuditCmd(),
	)
	addV6PersistentFlags(cmd)
	return cmd
}

func newConfigGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <key>",
		Short: "Read a config value (never writes)",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			ctx := context.Background()
			db, err := openV6DB(ctx)
			if err != nil {
				return err
			}
			defer db.Close()
			cfg := sqlite.NewConfigStore(db)
			warnConfigFootguns(ctx, cfg)
			return getOneConfig(ctx, cfg, args[0])
		},
	}
}

func newConfigSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Upsert a config row (requires both args)",
		Args:  cobra.ExactArgs(2),
		RunE: func(c *cobra.Command, args []string) error {
			ctx := context.Background()
			db, err := openV6DB(ctx)
			if err != nil {
				return err
			}
			defer db.Close()
			cfg := sqlite.NewConfigStore(db)
			warnConfigFootguns(ctx, cfg)
			return setOneConfig(ctx, cfg, args[0], args[1])
		},
	}
}

func newConfigAuditCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "audit",
		Short: "Report orphan keys left over from the pre-#16 ambiguous-positional CLI",
		Long: `Scans the config table for keys that match the footgun pattern
(e.g. "get", "set" — these were never meant to be stored values
but the pre-#16 CLI's positional form could create them). Prints
the matches with their values + suggested cleanup commands.

Non-destructive: this command only reads. Use ` + "`bmad config set`" + `
or a direct sqlite DELETE to remove them after review.`,
		RunE: func(c *cobra.Command, args []string) error {
			ctx := context.Background()
			db, err := openV6DB(ctx)
			if err != nil {
				return err
			}
			defer db.Close()
			cfg := sqlite.NewConfigStore(db)
			orphans, err := findConfigFootguns(ctx, cfg)
			if err != nil {
				return err
			}
			if len(orphans) == 0 {
				fmt.Println("config audit: clean (no orphan keys matching the #16 footgun pattern)")
				return nil
			}
			fmt.Printf("config audit: %d orphan key(s) found — likely from the pre-#16 ambiguous-positional CLI:\n\n", len(orphans))
			for _, e := range orphans {
				fmt.Printf("  %s = %q   (updated %s)\n", e.Key, e.Value, e.UpdatedAt.Format("2006-01-02 15:04:05"))
			}
			fmt.Println()
			fmt.Println("Suggested cleanup (review before running):")
			for _, e := range orphans {
				fmt.Printf("  sqlite3 bmad-state.db \"DELETE FROM config WHERE key = '%s';\"\n", e.Key)
			}
			return nil
		},
	}
}

// findConfigFootguns returns ConfigEntry rows whose key matches the pre-#16
// footgun list. Empty slice when the DB is clean.
func findConfigFootguns(ctx context.Context, cfg state.Config) ([]state.ConfigEntry, error) {
	all, err := cfg.All(ctx)
	if err != nil {
		return nil, err
	}
	want := make(map[string]bool, len(configFootgunKeys))
	for _, k := range configFootgunKeys {
		want[k] = true
	}
	var out []state.ConfigEntry
	for _, e := range all {
		if want[e.Key] {
			out = append(out, e)
		}
	}
	return out, nil
}

// warnConfigFootguns emits a one-line stderr nudge when a footgun key is
// present in the DB. Best-effort — if the audit itself errors, stay quiet so
// the parent command doesn't fail noisily on a diagnostic.
func warnConfigFootguns(ctx context.Context, cfg state.Config) {
	orphans, err := findConfigFootguns(ctx, cfg)
	if err != nil || len(orphans) == 0 {
		return
	}
	keys := make([]string, 0, len(orphans))
	for _, e := range orphans {
		keys = append(keys, e.Key)
	}
	fmt.Fprintf(os.Stderr,
		"NOTE: config table has %d orphan key(s) (%v) likely from the pre-#16 ambiguous-positional CLI. "+
			"Run `bmad config audit` for cleanup commands.\n",
		len(orphans), keys)
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
