package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/sosalejandro/bmad-story-runner-cli/domain/state"
	"github.com/sosalejandro/bmad-story-runner-cli/infrastructure/state/sqlite"
)

// newDepguardCmd is the parent of the `bmad depguard <verb>` namespace.
func newDepguardCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "depguard <verb>",
		Short: "Track per-rule warn→error flips for the golangci-depguard ratchet",
	}
	cmd.AddCommand(
		newDepguardFlipCmd(),
		newDepguardStatusCmd(),
		newDepguardHistoryCmd(),
	)
	addV6PersistentFlags(cmd)
	return cmd
}

// ---------- flip ----------

func newDepguardFlipCmd() *cobra.Command {
	var (
		to     string
		reason string
	)
	cmd := &cobra.Command{
		Use:   "flip <rule>",
		Short: "Flip a depguard rule's severity (default: warn → error)",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			ctx := context.Background()
			db, err := openV6DB(ctx)
			if err != nil {
				return err
			}
			defer db.Close()

			target := state.DepguardError
			switch to {
			case "", "error":
				target = state.DepguardError
			case "warn":
				target = state.DepguardWarn
			default:
				return fmt.Errorf("invalid --to %q (want warn|error)", to)
			}

			store := sqlite.NewDepguardStore(db)
			if err := store.Flip(ctx, args[0], target, reason); err != nil {
				return fmt.Errorf("depguard flip %q: %w", args[0], err)
			}
			fmt.Printf("%s -> %s\n", args[0], target)
			return nil
		},
	}
	cmd.Flags().StringVar(&to, "to", "error", "target severity: warn | error")
	cmd.Flags().StringVar(&reason, "reason", "", "optional reason captured in flip history")
	return cmd
}

// ---------- status ----------

func newDepguardStatusCmd() *cobra.Command {
	var raw bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show current per-rule severity",
		RunE: func(c *cobra.Command, args []string) error {
			ctx := context.Background()
			db, err := openV6DB(ctx)
			if err != nil {
				return err
			}
			defer db.Close()

			rows, err := sqlite.NewDepguardStore(db).All(ctx)
			if err != nil {
				return err
			}
			if raw {
				return json.NewEncoder(os.Stdout).Encode(rows)
			}
			if len(rows) == 0 {
				fmt.Println("(no rules flipped yet)")
				return nil
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "RULE\tSTATE\tFLIPPED")
			for _, r := range rows {
				fmt.Fprintf(w, "%s\t%s\t%s\n",
					r.Rule, r.State, r.FlippedAt.Format("2006-01-02 15:04"))
			}
			return w.Flush()
		},
	}
	cmd.Flags().BoolVar(&raw, "raw", false, "emit raw JSON")
	return cmd
}

// ---------- history ----------

func newDepguardHistoryCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "history <rule>",
		Short: "Show flip history for a rule (audit log)",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			ctx := context.Background()
			db, err := openV6DB(ctx)
			if err != nil {
				return err
			}
			defer db.Close()

			events, err := sqlite.NewDepguardStore(db).History(ctx, args[0])
			if err != nil {
				return err
			}
			if len(events) == 0 {
				if _, err := sqlite.NewDepguardStore(db).Get(ctx, args[0]); errors.Is(err, state.ErrNotFound) {
					fmt.Fprintf(os.Stderr, "depguard rule %q has never been flipped\n", args[0])
					os.Exit(1)
				}
			}
			return json.NewEncoder(os.Stdout).Encode(events)
		},
	}
}
