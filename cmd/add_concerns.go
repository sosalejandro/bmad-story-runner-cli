package cmd

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sosalejandro/bmad-story-runner-cli/application"
	appstate "github.com/sosalejandro/bmad-story-runner-cli/application/state"
	"github.com/sosalejandro/bmad-story-runner-cli/domain"
	"github.com/sosalejandro/bmad-story-runner-cli/infrastructure"
)

// addConcernsLongHelp documents the JSON schema for add-concerns input.
// Surfaced in the cobra Long field so operators don't have to grep the
// source for what fields QAConcern accepts (issue #71 sub-issue 3).
const addConcernsLongHelp = `Append QA concerns to a story.

Backend auto-detection (issue #71):
  - 2 positional args (<story-id> <concerns-json>)            → v6 SQLite store
  - 3 positional args with the first ending in .db / no ext  → v6 SQLite store,
    with the first arg overriding --state
  - 3 positional args with the first ending in .json         → legacy v4
    bmad-progress.json (back-compat)

JSON schema (v6 SQLite — each array element is stored as one
story_concerns row with body_json = the element serialized):

  Open-shaped object. Common fields:

    severity  string  (one of: low, medium, high, critical) — optional
    note      string  free-form description — optional
    stage     string  pipeline stage that surfaced the concern (e.g.
                      "code-review", "qa-gate") — optional
    finding   string  short summary suitable for a list table — optional

  Any additional keys are preserved verbatim in body_json. The --source
  flag tags the entire batch (defaults to "cli").

JSON schema (v4 legacy JSON store — strict):

  [{"severity":"<low|medium|high|critical>", "note":"<text>"}]

Examples:
  bmad add-concerns 1.1 '[{"severity":"high","note":"missing rollback test"}]'
  bmad add-concerns 1.1 '[{"stage":"code-review","finding":"flaky goroutine"}]' \
      --source code-review
  bmad add-concerns ./other-sprint.db 1.1 '[{"severity":"low","note":"nit"}]'`

func newAddConcernsCmd() *cobra.Command {
	var source string

	cmd := &cobra.Command{
		Use:   `add-concerns [<progress-json>] <story-id> <concerns-json>`,
		Short: "Append QA concerns to a story (auto-detects SQLite vs legacy JSON)",
		Long:  addConcernsLongHelp,
		Args:  cobra.RangeArgs(2, 3),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Two args: SQLite mode, args are <story-id> <concerns-json>.
			// Three args: arg[0] is the path; detect backend from it.
			var storyID, concernsJSON string
			backend := backendSQLite
			if len(args) == 2 {
				storyID, concernsJSON = args[0], args[1]
			} else {
				detected, _ := resolveStateBackend(args[0])
				backend = detected
				storyID, concernsJSON = args[1], args[2]
			}

			if backend == backendSQLite {
				return runAddConcernsSQLite(storyID, concernsJSON, source)
			}
			return runAddConcernsJSON(args[0], storyID, concernsJSON)
		},
	}

	cmd.Flags().StringVar(&source, "source", "cli",
		"provenance tag stored in story_concerns.source (SQLite mode only)")
	addV6PersistentFlags(cmd)

	return cmd
}

// runAddConcernsSQLite writes each element of the input array as a separate
// story_concerns row, tagged with --source (default "cli").
func runAddConcernsSQLite(storyID, concernsJSON, source string) error {
	var entries []appstate.AddConcernsInput
	if err := json.Unmarshal([]byte(concernsJSON), &entries); err != nil {
		return fmt.Errorf("parsing concerns JSON: %w", err)
	}

	ctx := context.Background()
	svc, cleanup, err := openStoryService(ctx)
	if err != nil {
		return err
	}
	defer cleanup()

	res, err := svc.AddConcerns(ctx, storyID, source, entries)
	if err != nil {
		return err
	}
	fmt.Printf("Added %d concern(s) to %s (source=%s)\n", res.Added, res.StoryID, res.Source)
	return nil
}

// runAddConcernsJSON is the legacy v4 path — preserved for back-compat
// with pre-SQLite-migration workflows.
func runAddConcernsJSON(progressPath, storyID, concernsJSON string) error {
	var concerns []domain.QAConcern
	if err := json.Unmarshal([]byte(concernsJSON), &concerns); err != nil {
		return fmt.Errorf("parsing concerns JSON: %w", err)
	}
	store := infrastructure.NewJSONProgressStore(log)
	uc := application.NewAddConcernsUseCase(store, log)
	if err := uc.Execute(progressPath, storyID, concerns); err != nil {
		return err
	}
	fmt.Printf("Added %d concern(s) to %s\n", len(concerns), storyID)
	return nil
}
