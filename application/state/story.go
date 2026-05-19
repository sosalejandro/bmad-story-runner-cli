package state

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	appsprint "github.com/sosalejandro/bmad-story-runner-cli/application/sprint"
	"github.com/sosalejandro/bmad-story-runner-cli/domain/state"
)

// StoryService composes the per-story ports for the cmd layer.
type StoryService struct {
	Stories      state.Stories
	Dependencies state.StoryDependencies
	Affects      state.StoryAffects
	Concerns     state.StoryConcerns
	RetryCounts  state.StoryRetryCounts
	Config       state.Config
	Checkpoints  state.Checkpoints
}

// NextAction is one row in the `story next` parallel-eligible batch.
type NextAction struct {
	StoryID    string  `json:"story_id"`
	Stage      string  `json:"stage"`
	HasEnv     bool    `json:"has_env"`
	Title      string  `json:"title"`
	Complexity string  `json:"complexity"`
	Affects    []string `json:"affects"`
}

// Next returns up to max stories that are unblocked AND non-overlapping in
// `affects` paths (file-overlap detection from §12 / sprint planning).
//
// Eligibility:
//   - status = pending
//   - every depends_on story has status = complete
//
// File-overlap detection: a story is added to the batch only if its affects
// set is disjoint from every story already chosen.
//
// Equivalent to NextWithScope(ctx, max, "").
func (s *StoryService) Next(ctx context.Context, max int) ([]NextAction, error) {
	return s.NextWithScope(ctx, max, "")
}

// NextWithScope is Next with an optional --scope <epic-id> per-call filter
// (issue #35). When scope is non-empty, only stories matching the epic
// (prefix + dot rule from appsprint.StoryMatchesEpic) are considered as
// candidates. An empty scope is a no-op filter — behaviour identical to
// Next. The filter is applied to the candidate set BEFORE dependency /
// file-overlap selection, so it never mutates state and never widens the
// returned batch beyond the scoped slice.
//
// A scope that matches zero stories returns an empty batch (no error).
// That mirrors the "nothing dispatchable right now" shape callers already
// handle for the unscoped case.
func (s *StoryService) NextWithScope(ctx context.Context, max int, scope string) ([]NextAction, error) {
	if max <= 0 {
		max = s.effectiveMaxParallel(ctx)
	}

	pending := state.StatusPending
	candidates, err := s.Stories.List(ctx, state.StoryFilter{Status: &pending})
	if err != nil {
		return nil, fmt.Errorf("story next list pending: %w", err)
	}

	var out []NextAction
	taken := make(map[string]bool) // path-keyed file claims

	for _, st := range candidates {
		if len(out) >= max {
			break
		}
		// --scope filter — apply BEFORE the (more expensive) deps + affects
		// lookups so a tightly-scoped call short-circuits cleanly.
		if !appsprint.StoryMatchesEpic(st.ID, scope) {
			continue
		}
		ok, err := s.depsSatisfied(ctx, st.ID)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}

		affects, err := s.Affects.Of(ctx, st.ID)
		if err != nil {
			return nil, fmt.Errorf("story next affects %q: %w", st.ID, err)
		}
		if overlapsAny(affects, taken) {
			continue
		}
		for _, p := range affects {
			taken[p] = true
		}

		stage := string(state.StageHydrate)
		if st.CurrentStage != nil {
			stage = string(*st.CurrentStage)
		}
		out = append(out, NextAction{
			StoryID:    st.ID,
			Stage:      stage,
			HasEnv:     false, // populated by env-status caller if needed
			Title:      st.Title,
			Complexity: string(st.Complexity),
			Affects:    affects,
		})
	}
	return out, nil
}

func (s *StoryService) depsSatisfied(ctx context.Context, storyID string) (bool, error) {
	deps, err := s.Dependencies.Of(ctx, storyID)
	if err != nil {
		return false, fmt.Errorf("story deps %q: %w", storyID, err)
	}
	for _, dep := range deps {
		st, err := s.Stories.Get(ctx, dep)
		if err != nil {
			return false, nil // missing dep = not satisfied
		}
		if st.Status != state.StatusComplete {
			return false, nil
		}
	}
	return true, nil
}

func overlapsAny(paths []string, taken map[string]bool) bool {
	for _, p := range paths {
		if taken[p] {
			return true
		}
	}
	return false
}

func (s *StoryService) effectiveMaxParallel(ctx context.Context) int {
	v, err := s.Config.Get(ctx, "max_parallel")
	if err != nil {
		return 4
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return 4
	}
	return n
}

// CheckpointResult describes whether §12.5 fired and which trigger.
type CheckpointResult struct {
	Fired         bool                     `json:"fired"`
	TriggerKind   state.CheckpointTrigger  `json:"trigger_kind,omitempty"`
	TriggerDetail string                   `json:"trigger_detail,omitempty"`
	CheckpointID  int64                    `json:"checkpoint_id,omitempty"`
}

// EvaluateCheckpoint runs §12.5 dual-trigger logic after a story completion.
//
//   - count trigger: stories_since_last >= config.checkpoint.count_threshold
//   - complexity trigger: just-completed story has complexity = high
func (s *StoryService) EvaluateCheckpoint(ctx context.Context, justCompletedID string) (*CheckpointResult, error) {
	just, err := s.Stories.Get(ctx, justCompletedID)
	if err != nil {
		return nil, fmt.Errorf("checkpoint get story: %w", err)
	}
	since, err := s.Checkpoints.StoriesSinceLast(ctx)
	if err != nil {
		return nil, err
	}
	threshold := s.checkpointThreshold(ctx)

	var (
		fired    bool
		kind     state.CheckpointTrigger
		detail   string
	)
	switch {
	case just.Complexity == state.ComplexityHigh:
		fired = true
		kind = state.CheckpointComplexity
		detail = "complexity:" + just.ID
	case since >= threshold:
		fired = true
		kind = state.CheckpointCount
	}

	if !fired {
		return &CheckpointResult{Fired: false}, nil
	}

	// Generate an idempotency key tied to the trigger semantics. Duplicate
	// fires (e.g., on crash resume) hit the UNIQUE index, return ErrAlready,
	// and we treat it as a no-op (the prior fire still stands).
	key := checkpointKey(kind, justCompletedID, since)

	id, err := s.Checkpoints.Fire(ctx, state.Checkpoint{
		TriggerKind:      kind,
		TriggerDetail:    ptrIfNonEmpty(detail),
		StoriesSinceLast: since,
		SummaryJSON:      `{"pending":"summary rendered by story-checkpoint skill"}`,
		IdempotencyKey:   &key,
	})
	if err != nil {
		if isUniqueViolation(err) {
			// Idempotency hit — the orchestrator already fired this exact
			// checkpoint trigger. Surface the existing row instead.
			return &CheckpointResult{
				Fired: false, TriggerKind: kind, TriggerDetail: detail,
			}, nil
		}
		return nil, err
	}
	return &CheckpointResult{
		Fired: true, TriggerKind: kind, TriggerDetail: detail, CheckpointID: id,
	}, nil
}

// checkpointKey builds a deterministic idempotency key tied to the trigger.
//
//   - complexity: "complexity:<story_id>"  (one fire per high-complexity story)
//   - count:      "count:<since>:<just_completed_id>"  (re-firing for the same
//     post-completion state is the only crash-resume case to dedup)
func checkpointKey(kind state.CheckpointTrigger, storyID string, since int) string {
	switch kind {
	case state.CheckpointComplexity:
		return "complexity:" + storyID
	case state.CheckpointCount:
		return fmt.Sprintf("count:%d:%s", since, storyID)
	}
	return fmt.Sprintf("%s:%s", kind, storyID)
}

func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "UNIQUE constraint failed")
}

func (s *StoryService) checkpointThreshold(ctx context.Context) int {
	v, err := s.Config.Get(ctx, "checkpoint.count_threshold")
	if err != nil {
		return 4
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return 4
	}
	return n
}

func ptrIfNonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
