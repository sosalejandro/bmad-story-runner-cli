package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	appstate "github.com/sosalejandro/bmad-story-runner-cli/application/state"
	"github.com/sosalejandro/bmad-story-runner-cli/domain/state"
)

// storyRowJSON is the per-row shape of the `bmad story status` (no-id)
// listing under --json. We expose every column the human table includes
// plus a few extras (claim audit, parallel group) that downstream
// orchestrators commonly want — the cost of including them is one
// pointer-deref each, and forcing a second round-trip to fetch them
// would be the worse trade.
type storyRowJSON struct {
	ID            string  `json:"id"`
	Status        string  `json:"status"`
	Stage         *string `json:"stage"`
	Complexity    string  `json:"complexity"`
	StoryType     string  `json:"story_type"`
	CIPassed      bool    `json:"ci_passed"`
	Title         string  `json:"title"`
	ParallelGroup *int    `json:"parallel_group"`
	ClaimedAt     *string `json:"claimed_at"`
	ClaimedBy     *string `json:"claimed_by"`
}

func storyRowJSONFrom(st state.Story) storyRowJSON {
	row := storyRowJSON{
		ID:            st.ID,
		Status:        string(st.Status),
		Complexity:    string(st.Complexity),
		StoryType:     string(st.StoryType),
		CIPassed:      st.CIPassed,
		Title:         st.Title,
		ParallelGroup: st.ParallelGroup,
		ClaimedBy:     st.ClaimedBy,
	}
	if st.CurrentStage != nil {
		s := string(*st.CurrentStage)
		row.Stage = &s
	}
	if st.ClaimedAt != nil {
		t := st.ClaimedAt.UTC().Format("2006-01-02T15:04:05Z")
		row.ClaimedAt = &t
	}
	return row
}

// emitStoryListJSON powers `bmad story status --json` (no positional id).
// `result` is an array of storyRowJSON so JQ queries can use `.result | map(...)`
// directly; we deliberately do NOT wrap in an object with a "count" — the
// envelope's result is the natural place for the array, and `length` gives
// the count.
func emitStoryListJSON(
	ctx context.Context,
	svc *appstate.StoryService,
	c *cobra.Command,
	f state.StoryFilter,
	statusFlag, stageFlag, scopeFlag string,
	hasEnvSet, hasEnv bool,
) error {
	rows, err := svc.Stories.List(ctx, f)
	if err != nil {
		return err
	}
	rows = filterStoriesByScope(rows, scopeFlag)
	out := make([]storyRowJSON, 0, len(rows))
	for _, st := range rows {
		out = append(out, storyRowJSONFrom(st))
	}
	args := map[string]any{}
	if statusFlag != "" {
		args["status"] = statusFlag
	}
	if stageFlag != "" {
		args["stage"] = stageFlag
	}
	if scopeFlag != "" {
		args["scope"] = scopeFlag
	}
	if hasEnvSet {
		args["has_env"] = hasEnv
	}
	return emitJSONStdout(commandPathSansRoot(c), args, out, nil)
}

// emitStoryDetailJSON powers `bmad story status <id> --json`. The result
// is a single storyDetailJSON object (NOT wrapped in an array) so callers
// can `.result.id` directly. Mirrors the keys printStoryDetail prints to
// stdout in non-JSON mode — but emitted under the v1 envelope so it
// composes with the rest of the JSON contract.
func emitStoryDetailJSON(ctx context.Context, svc *appstate.StoryService, c *cobra.Command, id string) error {
	st, err := svc.Stories.Get(ctx, id)
	if errors.Is(err, state.ErrNotFound) {
		// Match the human-mode UX: print a stderr message and exit 1.
		// We still emit a small envelope to stdout so a downstream JQ
		// chain sees "result": null + a warning, instead of an empty
		// stream that's harder to spot in logs.
		_ = emitJSONStdout(commandPathSansRoot(c),
			map[string]any{"id": id},
			nil,
			[]string{fmt.Sprintf("story %q not found", id)},
		)
		fmt.Fprintf(os.Stderr, "story %q not found\n", id)
		os.Exit(1)
	}
	if err != nil {
		return err
	}
	deps, _ := svc.Dependencies.Of(ctx, id)
	affects, _ := svc.Affects.Of(ctx, id)
	concerns, _ := svc.Concerns.Of(ctx, id)
	retries, _ := svc.RetryCounts.Get(ctx, id)

	result := map[string]any{
		"id":               st.ID,
		"file":             st.File,
		"title":            st.Title,
		"status":           string(st.Status),
		"current_stage":    stageStr(st.CurrentStage),
		"complexity":       string(st.Complexity),
		"story_type":       string(st.StoryType),
		"parallel_group":   st.ParallelGroup,
		"hydrated_file":    st.HydratedFile,
		"resource_budget":  st.ResourceBudget,
		"requires_android": st.RequiresAndroid,
		"ci_passed":        st.CIPassed,
		"commit_hash":      st.CommitHash,
		"pr_url":           st.PRURL,
		"completed_at":     st.CompletedAt,
		"created_at":       st.CreatedAt,
		"updated_at":       st.UpdatedAt,
		"depends_on":       deps,
		"affects":          affects,
		"concerns":         concerns,
		"retry_counts":     retries,
		"claimed_at":       st.ClaimedAt,
		"claimed_by":       st.ClaimedBy,
	}
	return emitJSONStdout(commandPathSansRoot(c), map[string]any{"id": id}, result, nil)
}

func stageStr(s *state.Stage) *string {
	if s == nil {
		return nil
	}
	v := string(*s)
	return &v
}
