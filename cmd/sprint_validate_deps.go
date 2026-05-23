package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	appsprint "github.com/sosalejandro/bmad-story-runner-cli/application/sprint"
	"github.com/sosalejandro/bmad-story-runner-cli/cmd/exitcode"
	"github.com/sosalejandro/bmad-story-runner-cli/infrastructure/state/sqlite"
)

// newSprintValidateDepsCmd wires `bmad sprint validate-deps` — closes #48.
//
// Detection model (delegated to application/sprint.Validate):
//
//   - cycle              (ERROR — graph is broken)
//   - missing_dep        (ERROR — depends_on points at a non-existent id)
//   - orphan             (WARN  — no dependents AND not last of epic;
//                         escalated to ERROR via --strict)
//   - placeholder_smell  (INFO  — first-of-epic depends only on last-of-prev)
//   - diamond            (INFO  — multiple paths converge on one ancestor)
//
// Exit code contract:
//
//   exitcode.Success (0)           — no blocking findings
//   exitcode.ValidationError (30)  — any error (or warn when --strict)
//
// We deliberately keep the orphan default at warn (exit 0): orchestrators
// running validate-deps as a periodic check shouldn't see CI go red over
// what's usually a side-quest doc story. Operators who want strict gates
// (PR pre-merge hook, etc.) opt in with --strict.
func newSprintValidateDepsCmd() *cobra.Command {
	var (
		epicsPath string
		scope     string
		strict    bool
	)
	cmd := &cobra.Command{
		Use:   "validate-deps",
		Short: "Detect cycles, orphans, missing deps, and placeholder-smell in epics.md (closes #48)",
		Long: `Walks the dependency graph parsed from epics.md and reports:

  cycle              ERROR  A->B->...->A walks; ` + "`bmad story next`" + ` would stall.
  missing_dep        ERROR  ` + "`depends_on`" + ` points at a non-existent story id.
  orphan             WARN   Story has no successors AND is not last of its epic
                            (often a missed downstream linkage). --strict
                            escalates to ERROR.
  placeholder_smell  INFO   First story of Epic N depends only on last of Epic
                            N-1, with no epic-level ` + "`requires_epics:`" + ` declared
                            — likely a conservative linear placeholder rather
                            than a real semantic dependency.
  diamond            INFO   Two+ dep-paths converge on a shared ancestor.
                            Sometimes legit (synchronisation point); flagged
                            so the operator can decide.

Exit codes (` + "`bmad doctor`" + ` shows the full contract):

  0                          no blocking findings
  30 (VALIDATION_ERROR)      one or more ERROR findings (or WARN + --strict)`,
		RunE: func(c *cobra.Command, args []string) error {
			ctx := context.Background()
			// Resolve epicsPath from config.docs_folder when omitted, mirroring
			// the convention of `bmad sprint plan` and `bmad sprint infer-deps`.
			if epicsPath == "" {
				db, err := openV6DB(ctx)
				if err != nil {
					return err
				}
				defer db.Close()
				cfg := sqlite.NewConfigStore(db)
				docs, err := cfg.Get(ctx, "docs_folder")
				if err != nil {
					return fmt.Errorf("sprint validate-deps: --epics required (docs_folder not set: %w)", err)
				}
				epicsPath = filepath.Join(docs, "epics.md")
			}

			parsed, err := appsprint.ParseEpicsFile(epicsPath)
			if err != nil {
				return err
			}

			// Post-#54: consult the state DB for any edges the planner
			// has already persisted as synthesised (edge_kind !=
			// 'explicit'). Each such edge suppresses the placeholder-
			// smell finding on the matching (story, dep) pair.
			//
			// Failure modes: when the DB isn't reachable (fresh repo
			// pre-`bmad sprint plan`), or when no rows have been written
			// yet, we proceed with a nil edge set — every linear-chain
			// pattern still emits the INFO finding, same conservative
			// behaviour as the pre-#54 nil EpicRequires path.
			syntheticEdges := loadSyntheticEdges(ctx)

			report := appsprint.Validate(parsed, appsprint.ValidateOptions{
				Scope:          scope,
				Strict:         strict,
				SyntheticEdges: syntheticEdges,
			})

			if jsonOutput {
				cmdArgs := map[string]any{
					"epics":  epicsPath,
					"strict": strict,
				}
				if scope != "" {
					cmdArgs["scope"] = scope
				}
				if err := emitJSONStdout(commandPathSansRoot(c), cmdArgs, report, nil); err != nil {
					return err
				}
			} else {
				emitValidateText(os.Stdout, report)
			}

			if report.HasBlockingFindings() {
				// Exit with the stable validation code so orchestrators can
				// branch on it. We bypass exitOnError's generic exit-1 mapping
				// by os.Exit'ing directly; cobra's RunE return is intentionally
				// suppressed to nil so the wrapper doesn't double-print.
				os.Exit(exitcode.ValidationError.Int())
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&epicsPath, "epics", "", "epics.md path (default: <docs_folder>/epics.md)")
	cmd.Flags().StringVar(&scope, "scope", "", "restrict validation to one epic id (e.g. 4 → only 4.* stories)")
	cmd.Flags().BoolVar(&strict, "strict", false, "escalate WARN findings (orphans) to errors for the exit-code decision")
	return cmd
}

// loadSyntheticEdges queries the state DB for every story_dependencies row
// whose edge_kind marks it as a synth product of issue #46 / #54 epic-
// level resolution, and returns them shaped for the validator's
// suppression lookup. Errors are swallowed deliberately — a fresh repo
// without a DB on disk simply gets a nil set, the same defensive default
// every other code path uses for "no DB context available".
//
// This is what makes `bmad sprint validate-deps` distinguish a story
// author's depends_on placeholder from a planner-synthesised edge: the
// DB is the single source of truth for that distinction, and the row
// shape on disk is what we read here.
func loadSyntheticEdges(ctx context.Context) appsprint.SyntheticEdgeSet {
	db, err := openV6DB(ctx)
	if err != nil {
		return nil
	}
	defer db.Close()
	rows, err := sqlite.QuerySyntheticEdges(ctx, db)
	if err != nil {
		return nil
	}
	if len(rows) == 0 {
		return nil
	}
	out := make(appsprint.SyntheticEdgeSet, len(rows))
	for _, r := range rows {
		inner := out[r.StoryID]
		if inner == nil {
			inner = make(map[string]bool, 2)
			out[r.StoryID] = inner
		}
		inner[r.DependsOnID] = true
	}
	return out
}

// emitValidateText renders a short human-readable summary. JSON callers
// take the envelope path instead.
func emitValidateText(w *os.File, r appsprint.ValidateReport) {
	if len(r.Findings) == 0 {
		fmt.Fprintf(w, "OK — %d stories validated, no findings\n", r.TotalStories)
		return
	}
	fmt.Fprintf(w, "%d findings across %d stories (errors=%d warnings=%d info=%d)\n",
		len(r.Findings), r.TotalStories, r.Counts.Error, r.Counts.Warn, r.Counts.Info)
	if r.Scope != "" {
		fmt.Fprintf(w, "scope: %s\n", r.Scope)
	}
	for _, f := range r.Findings {
		fmt.Fprintf(w, "  [%s] %s: %s\n", f.Severity, f.Kind, f.Message)
		if f.SuggestedFix != "" {
			fmt.Fprintf(w, "      fix: %s\n", f.SuggestedFix)
		}
	}
}
