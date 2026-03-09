package application

import (
	"fmt"

	"go.uber.org/zap"

	"github.com/sosalejandro/bmad-story-runner-cli/application/ports"
	"github.com/sosalejandro/bmad-story-runner-cli/domain"
)

type ListStoriesUseCase struct {
	store ports.ProgressStore
	log   *zap.Logger
}

func NewListStoriesUseCase(store ports.ProgressStore, log *zap.Logger) *ListStoriesUseCase {
	return &ListStoriesUseCase{store: store, log: log}
}

// ListFilter controls which stories are returned.
type ListFilter struct {
	Group         *int
	Status        *domain.Status
	UnblockedOnly bool
}

// BlockerInfo describes a blocker and whether it is resolved.
type BlockerInfo struct {
	ID       string
	Resolved bool
}

// ListRow is one row in the list output.
type ListRow struct {
	Story    *domain.Story
	Blockers []BlockerInfo
}

func (uc *ListStoriesUseCase) Execute(progressPath string, filter ListFilter) ([]ListRow, error) {
	progress, err := uc.store.Load(progressPath)
	if err != nil {
		return nil, fmt.Errorf("loading progress file %q: %w", progressPath, err)
	}

	var rows []ListRow

	for _, s := range progress.Stories {
		if filter.Group != nil && (s.ParallelGroup == nil || *s.ParallelGroup != *filter.Group) {
			continue
		}
		if filter.Status != nil && s.Status != *filter.Status {
			continue
		}

		var blockers []BlockerInfo
		allResolved := true
		for _, b := range s.Blockers {
			resolved := false
			if found := progress.FindByID(b); found != nil && found.Status == domain.StatusComplete {
				resolved = true
			}
			if !resolved {
				allResolved = false
			}
			blockers = append(blockers, BlockerInfo{ID: b, Resolved: resolved})
		}

		if filter.UnblockedOnly && !allResolved {
			continue
		}

		rows = append(rows, ListRow{Story: s, Blockers: blockers})
	}

	uc.log.Debug("list stories",
		zap.Int("matched", len(rows)),
		zap.Int("total", len(progress.Stories)),
	)

	return rows, nil
}
