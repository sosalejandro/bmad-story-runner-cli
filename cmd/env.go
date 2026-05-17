package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	appenv "github.com/sosalejandro/bmad-story-runner-cli/application/env"
	"github.com/sosalejandro/bmad-story-runner-cli/domain/state"
	"github.com/sosalejandro/bmad-story-runner-cli/infrastructure/state/sqlite"
)

// newEnvCmd is the parent of the `bmad env <verb>` namespace.
func newEnvCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "env <verb>",
		Short: "Per-story test infrastructure (port pool + docker delegation + sweeper)",
	}
	cmd.AddCommand(
		newEnvUpCmd(),
		newEnvDownCmd(),
		newEnvStatusCmd(),
		newEnvCleanupOrphansCmd(),
	)
	addV6PersistentFlags(cmd)
	return cmd
}

// ---------- env up ----------

func newEnvUpCmd() *cobra.Command {
	var configDir string
	cmd := &cobra.Command{
		Use:   "up <story-id>",
		Short: "Allocate ports + write env_allocations + emit env config (docker-up is caller's job)",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			ctx := context.Background()
			db, err := openV6DB(ctx)
			if err != nil {
				return err
			}
			defer db.Close()

			if configDir == "" {
				configDir = "."
			}
			tec, err := appenv.LoadTestEnvConfig(configDir)
			if err != nil {
				return err
			}
			pool := appenv.NewPortPool(tec, sqlite.NewEnvsStore(db))
			alloc, err := pool.Allocate(ctx, args[0])
			if err != nil {
				return err
			}
			return json.NewEncoder(os.Stdout).Encode(envJSON(alloc))
		},
	}
	cmd.Flags().StringVar(&configDir, "config-dir", "", "directory containing .bmad-test-env.yml (default: cwd)")
	return cmd
}

func envJSON(a state.EnvAllocation) map[string]any {
	out := map[string]any{
		"story_id":   a.StoryID,
		"pg_port":    a.PGPort,
		"redis_port": a.RedisPort,
		"db_name":    a.DBName,
	}
	if a.OtelPort != nil {
		out["otel_port"] = *a.OtelPort
	}
	if len(a.ContainerIDs) > 0 {
		out["container_ids"] = a.ContainerIDs
	}
	return out
}

// ---------- env down ----------

func newEnvDownCmd() *cobra.Command {
	var reason string
	cmd := &cobra.Command{
		Use:   "down <story-id>",
		Short: "Release env_allocation (port pool slot freed; docker-compose down is caller's job)",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			ctx := context.Background()
			db, err := openV6DB(ctx)
			if err != nil {
				return err
			}
			defer db.Close()
			envs := sqlite.NewEnvsStore(db)
			if err := envs.Release(ctx, args[0], reason); err != nil {
				return fmt.Errorf("env down %q: %w", args[0], err)
			}
			fmt.Printf("%s -> released (%s)\n", args[0], reason)
			return nil
		},
	}
	cmd.Flags().StringVar(&reason, "reason", "completed", "release reason: completed | stale | manual")
	return cmd
}

// ---------- env status ----------

func newEnvStatusCmd() *cobra.Command {
	var raw bool
	cmd := &cobra.Command{
		Use:   "status [<story-id>]",
		Short: "Show active env allocations (or detail one)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			ctx := context.Background()
			db, err := openV6DB(ctx)
			if err != nil {
				return err
			}
			defer db.Close()
			envs := sqlite.NewEnvsStore(db)

			if len(args) == 1 {
				a, err := envs.Get(ctx, args[0])
				if errors.Is(err, state.ErrNotFound) {
					fmt.Fprintf(os.Stderr, "env %q not found\n", args[0])
					os.Exit(1)
				}
				if err != nil {
					return err
				}
				return json.NewEncoder(os.Stdout).Encode(envJSON(a))
			}

			active, err := envs.Active(ctx)
			if err != nil {
				return err
			}
			if raw {
				return json.NewEncoder(os.Stdout).Encode(active)
			}
			if len(active) == 0 {
				fmt.Println("(no active envs)")
				return nil
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "STORY\tPG\tREDIS\tOTEL\tDB\tCREATED")
			for _, a := range active {
				otel := "-"
				if a.OtelPort != nil {
					otel = fmt.Sprintf("%d", *a.OtelPort)
				}
				fmt.Fprintf(w, "%s\t%d\t%d\t%s\t%s\t%s\n",
					a.StoryID, a.PGPort, a.RedisPort, otel, a.DBName,
					a.CreatedAt.Format("2006-01-02 15:04"))
			}
			return w.Flush()
		},
	}
	cmd.Flags().BoolVar(&raw, "raw", false, "emit raw JSON instead of table")
	return cmd
}

// ---------- env cleanup-orphans ----------

func newEnvCleanupOrphansCmd() *cobra.Command {
	var threshold int
	cmd := &cobra.Command{
		Use:   "cleanup-orphans",
		Short: "Activity-based sweep (§12.6): release envs with no recent dispatch + no recent worktree edits",
		RunE: func(c *cobra.Command, args []string) error {
			ctx := context.Background()
			db, err := openV6DB(ctx)
			if err != nil {
				return err
			}
			defer db.Close()
			sweeper := appenv.NewSweeper(
				sqlite.NewEnvsStore(db),
				sqlite.NewWorktreesStore(db),
				sqlite.NewDispatchesStore(db),
				sqlite.NewConfigStore(db),
			)
			res, err := sweeper.Sweep(ctx, threshold)
			if err != nil {
				return err
			}
			return json.NewEncoder(os.Stdout).Encode(res)
		},
	}
	cmd.Flags().IntVar(&threshold, "threshold-minutes", 0,
		"override env.stale_threshold_minutes config")
	return cmd
}
