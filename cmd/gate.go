package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/sosalejandro/bmad-story-runner-cli/domain/state"
	"github.com/sosalejandro/bmad-story-runner-cli/infrastructure/state/sqlite"
)

// newGateCmd is the parent of the v6 `bmad gate <verb>` namespace.
//
// V4 had `bmad write-gate / gate-check / reconcile` writing YAML files;
// V6 records gate decisions in story_concerns + flips story.status atomically.
// The yaml-on-disk format is reserved for compat-mode reads via `bmad migrate`.
func newGateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "gate <verb>",
		Short: "QA gate decisions (PASS / FAIL / CONCERNS) written to story state",
	}
	cmd.AddCommand(
		newGateWriteCmd(),
		newGateCheckSubCmd(),
	)
	addV6PersistentFlags(cmd)
	return cmd
}

// ---------- write ----------

func newGateWriteCmd() *cobra.Command {
	var (
		concernsJSON string
		source       string
	)
	cmd := &cobra.Command{
		Use:   "write <story-id> <PASS|FAIL|CONCERNS>",
		Short: "Record a gate decision; updates story status + appends concerns row",
		Args:  cobra.ExactArgs(2),
		RunE: func(c *cobra.Command, args []string) error {
			ctx := context.Background()
			db, err := openV6DB(ctx)
			if err != nil {
				return err
			}
			defer db.Close()

			id := args[0]
			decision := strings.ToUpper(args[1])

			stories := sqlite.NewStoriesStore(db)
			concerns := sqlite.NewStoryConcernsStore(db)

			switch decision {
			case "PASS":
				if err := stories.SetStatus(ctx, id, state.StatusComplete); err != nil {
					return err
				}
			case "FAIL":
				if err := stories.SetStatus(ctx, id, state.StatusBlocked); err != nil {
					return err
				}
				if concernsJSON != "" {
					if err := concerns.Add(ctx, id, sourceOrDefault(source), concernsJSON); err != nil {
						return err
					}
				}
			case "CONCERNS":
				if concernsJSON == "" {
					return fmt.Errorf("CONCERNS gate requires --concerns <json-array>")
				}
				if err := concerns.Add(ctx, id, sourceOrDefault(source), concernsJSON); err != nil {
					return err
				}
			default:
				return fmt.Errorf("unknown gate decision %q (want PASS|FAIL|CONCERNS)", args[1])
			}

			fmt.Printf("%s -> %s\n", id, decision)
			return nil
		},
	}
	cmd.Flags().StringVar(&concernsJSON, "concerns", "", "JSON array of concerns (required for CONCERNS; optional for FAIL)")
	cmd.Flags().StringVar(&source, "source", "", "concern source (default: 'gate-cli')")
	return cmd
}

func sourceOrDefault(s string) string {
	if s == "" {
		return "gate-cli"
	}
	return s
}

// ---------- check ----------

func newGateCheckSubCmd() *cobra.Command {
	var raw bool
	cmd := &cobra.Command{
		Use:   "check",
		Short: "List stories blocked at the QA gate + their concerns",
		RunE: func(c *cobra.Command, args []string) error {
			ctx := context.Background()
			db, err := openV6DB(ctx)
			if err != nil {
				return err
			}
			defer db.Close()
			stories := sqlite.NewStoriesStore(db)
			concerns := sqlite.NewStoryConcernsStore(db)

			blocked := state.StatusBlocked
			rows, err := stories.List(ctx, state.StoryFilter{Status: &blocked})
			if err != nil {
				return err
			}
			if raw {
				type report struct {
					StoryID  string           `json:"story_id"`
					Title    string           `json:"title"`
					Concerns []state.Concern  `json:"concerns"`
				}
				var out []report
				for _, st := range rows {
					cs, _ := concerns.Of(ctx, st.ID)
					out = append(out, report{StoryID: st.ID, Title: st.Title, Concerns: cs})
				}
				return json.NewEncoder(os.Stdout).Encode(out)
			}
			if len(rows) == 0 {
				fmt.Println("(no blocked stories)")
				return nil
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "STORY\tCONCERNS\tTITLE")
			for _, st := range rows {
				cs, _ := concerns.Of(ctx, st.ID)
				fmt.Fprintf(w, "%s\t%d\t%s\n", st.ID, len(cs), st.Title)
			}
			return w.Flush()
		},
	}
	cmd.Flags().BoolVar(&raw, "raw", false, "emit detailed JSON including each concern body")
	return cmd
}
