package state

import (
	"context"
	"fmt"

	"github.com/sosalejandro/bmad-story-runner-cli/domain/state"
)

// ListFilter mirrors the v4 application.ListFilter shape against the v6
// state store. Empty pointer fields are "any".
//
// UnblockedOnly resolves "blockers" via story_dependencies: a v6 story is
// considered unblocked when every depends_on edge points at a story whose
// status = complete.
type ListFilter struct {
	Status        *state.Status
	ParallelGroup *int
	UnblockedOnly bool
}

// ListBlocker is one dependency edge enriched with its resolution status,
// suitable for the table-print path.
type ListBlocker struct {
	ID       string `json:"id"`
	Resolved bool   `json:"resolved"`
}

// ListRow is one story plus its blocker resolution snapshot.
type ListRow struct {
	Story    state.Story   `json:"story"`
	Blockers []ListBlocker `json:"blockers"`
}

// List streams the SQLite stories table through the filter set and returns
// the enriched rows. Each row is decorated with its full dependency list +
// resolution markers so callers can either print blockers (--show-blockers)
// or filter by unblocked-only without a second round trip.
//
// The implementation is intentionally one query per story for the
// blocker enrichment — the typical sprint has dozens to a few hundred
// stories, and StoryDependencies.Of is a pinpoint SQL lookup. If profiling
// shows this is a hot path on multi-thousand-story sprints, a bulk
// "DependenciesForAll" port can be added without changing this signature.
func (s *StoryService) List(ctx context.Context, f ListFilter) ([]ListRow, error) {
	dbFilter := state.StoryFilter{}
	if f.Status != nil {
		dbFilter.Status = f.Status
	}
	if f.ParallelGroup != nil {
		dbFilter.ParallelGroup = f.ParallelGroup
	}

	stories, err := s.Stories.List(ctx, dbFilter)
	if err != nil {
		return nil, fmt.Errorf("list stories: %w", err)
	}

	// statusByID caches lookups so a story with N deps doesn't issue N
	// extra Stories.Get round trips when deps overlap across many rows
	// (which is the common case in a sprint).
	statusByID := make(map[string]state.Status, len(stories))
	for _, st := range stories {
		statusByID[st.ID] = st.Status
	}

	rows := make([]ListRow, 0, len(stories))
	for _, st := range stories {
		deps, err := s.Dependencies.Of(ctx, st.ID)
		if err != nil {
			return nil, fmt.Errorf("list stories: deps for %s: %w", st.ID, err)
		}
		blockers := make([]ListBlocker, 0, len(deps))
		allResolved := true
		for _, depID := range deps {
			status, ok := statusByID[depID]
			if !ok {
				// Dep points at a story not in the current listing — go
				// look it up directly so the resolution check is correct.
				dep, derr := s.Stories.Get(ctx, depID)
				if derr == nil {
					status = dep.Status
					statusByID[depID] = status
				}
			}
			resolved := status == state.StatusComplete
			if !resolved {
				allResolved = false
			}
			blockers = append(blockers, ListBlocker{ID: depID, Resolved: resolved})
		}
		if f.UnblockedOnly && !allResolved {
			continue
		}
		rows = append(rows, ListRow{Story: st, Blockers: blockers})
	}
	return rows, nil
}
