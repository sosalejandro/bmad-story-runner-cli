package application

import (
	"fmt"

	"go.uber.org/zap"

	"github.com/sosalejandro/bmad-story-runner-cli/application/ports"
	"github.com/sosalejandro/bmad-story-runner-cli/domain"
)

type GetStatusUseCase struct {
	store ports.ProgressStore
	log   *zap.Logger
}

func NewGetStatusUseCase(store ports.ProgressStore, log *zap.Logger) *GetStatusUseCase {
	return &GetStatusUseCase{store: store, log: log}
}

type StatusSummary struct {
	Progress *domain.ProgressFile
	Counts   map[domain.Status]int
	Groups   map[int][]*domain.Story
}

func (uc *GetStatusUseCase) Execute(progressPath string) (*StatusSummary, error) {
	progress, err := uc.store.Load(progressPath)
	if err != nil {
		return nil, fmt.Errorf("loading progress file %q: %w", progressPath, err)
	}

	counts := progress.StatusCounts()

	groups := make(map[int][]*domain.Story)
	for _, s := range progress.Stories {
		if s.ParallelGroup != nil {
			groups[*s.ParallelGroup] = append(groups[*s.ParallelGroup], s)
		}
	}

	uc.log.Debug("status loaded",
		zap.Int("total_stories", len(progress.Stories)),
		zap.Int("complete", counts[domain.StatusComplete]),
	)

	return &StatusSummary{Progress: progress, Counts: counts, Groups: groups}, nil
}
