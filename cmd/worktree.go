package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/sosalejandro/bmad-story-runner-cli/domain/state"
	"github.com/sosalejandro/bmad-story-runner-cli/infrastructure/state/sqlite"
)

// newWorktreeCmd is the parent of the `bmad worktree <verb>` namespace.
//
// IMPORTANT: this cmd records the worktree allocation in SQLite. The actual
// `git worktree add/remove` invocation is the caller's responsibility — the
// orchestrator agent runs git, the CLI tracks state. This keeps the CLI
// idempotent and the git work auditable in normal git history.
func newWorktreeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "worktree <verb>",
		Short: "Track per-story worktree allocations (git operations are caller's job)",
	}
	cmd.AddCommand(
		newWorktreeCreateCmd(),
		newWorktreeDestroyCmd(),
		newWorktreeListCmd(),
		newWorktreePruneCmd(),
	)
	addV6PersistentFlags(cmd)
	return cmd
}

// ---------- create ----------

func newWorktreeCreateCmd() *cobra.Command {
	var (
		baseDir    string
		branchName string
	)
	cmd := &cobra.Command{
		Use:   "create <story-id>",
		Short: "Record a worktree row + emit suggested git command",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			ctx := context.Background()
			db, err := openV6DB(ctx)
			if err != nil {
				return err
			}
			defer db.Close()

			id := args[0]
			if baseDir == "" {
				baseDir = ".worktrees"
			}
			if branchName == "" {
				branchName = "story/" + id
			}
			path := filepath.Join(baseDir, "story-"+id)

			w := state.Worktree{StoryID: id, Path: path, BranchName: branchName}
			store := sqlite.NewWorktreesStore(db)
			if err := store.Create(ctx, w); err != nil {
				return fmt.Errorf("worktree create %q: %w", id, err)
			}
			return json.NewEncoder(os.Stdout).Encode(map[string]string{
				"story_id":    id,
				"path":        path,
				"branch_name": branchName,
				"git_cmd":     fmt.Sprintf("git worktree add %s -b %s", path, branchName),
			})
		},
	}
	cmd.Flags().StringVar(&baseDir, "base-dir", "", "base directory for worktrees (default: .worktrees)")
	cmd.Flags().StringVar(&branchName, "branch", "", "branch name (default: story/<story-id>)")
	return cmd
}

// ---------- destroy ----------

func newWorktreeDestroyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "destroy <story-id>",
		Short: "Delete worktree row + emit suggested git command",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			ctx := context.Background()
			db, err := openV6DB(ctx)
			if err != nil {
				return err
			}
			defer db.Close()

			store := sqlite.NewWorktreesStore(db)
			w, err := store.Get(ctx, args[0])
			if errors.Is(err, state.ErrNotFound) {
				fmt.Fprintf(os.Stderr, "worktree %q not found\n", args[0])
				os.Exit(1)
			}
			if err != nil {
				return err
			}
			if err := store.Delete(ctx, args[0]); err != nil {
				return err
			}
			return json.NewEncoder(os.Stdout).Encode(map[string]string{
				"story_id": args[0],
				"git_cmd":  fmt.Sprintf("git worktree remove %s && git branch -D %s", w.Path, w.BranchName),
			})
		},
	}
}

// ---------- list ----------

func newWorktreeListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all recorded worktrees",
		RunE: func(c *cobra.Command, args []string) error {
			ctx := context.Background()
			db, err := openV6DB(ctx)
			if err != nil {
				return err
			}
			defer db.Close()
			store := sqlite.NewWorktreesStore(db)
			rows, err := store.List(ctx)
			if err != nil {
				return err
			}
			if len(rows) == 0 {
				fmt.Println("(no worktrees)")
				return nil
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "STORY\tPATH\tBRANCH\tLAST ACTIVITY")
			for _, wt := range rows {
				activity := "-"
				if wt.LastActivityAt != nil {
					activity = wt.LastActivityAt.Format("2006-01-02 15:04")
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
					wt.StoryID, wt.Path, wt.BranchName, activity)
			}
			return w.Flush()
		},
	}
}

// ---------- prune ----------

func newWorktreePruneCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "prune",
		Short: "Delete worktrees for stories whose status = complete",
		RunE: func(c *cobra.Command, args []string) error {
			ctx := context.Background()
			db, err := openV6DB(ctx)
			if err != nil {
				return err
			}
			defer db.Close()
			worktrees := sqlite.NewWorktreesStore(db)
			stories := sqlite.NewStoriesStore(db)

			all, err := worktrees.List(ctx)
			if err != nil {
				return err
			}

			var deleted []string
			for _, wt := range all {
				st, err := stories.Get(ctx, wt.StoryID)
				if errors.Is(err, state.ErrNotFound) {
					continue
				}
				if err != nil {
					return err
				}
				if st.Status != state.StatusComplete {
					continue
				}
				if err := worktrees.Delete(ctx, wt.StoryID); err != nil {
					return err
				}
				deleted = append(deleted, wt.StoryID)
			}

			return json.NewEncoder(os.Stdout).Encode(map[string]any{
				"deleted":  deleted,
				"pruned_at": time.Now().UTC().Format(time.RFC3339),
			})
		},
	}
}
