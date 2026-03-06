package application

import (
	"fmt"
	"path/filepath"
	"sort"

	"go.uber.org/zap"

	"github.com/sosalejandro/bmad-story-runner-cli/application/ports"
	"github.com/sosalejandro/bmad-story-runner-cli/domain"
)

type NextStoryUseCase struct {
	store ports.ProgressStore
	log   *zap.Logger
}

func NewNextStoryUseCase(store ports.ProgressStore, log *zap.Logger) *NextStoryUseCase {
	return &NextStoryUseCase{store: store, log: log}
}

// Execute returns the absolute file path of the next eligible story.
// If group is non-nil, only stories in that parallel group are considered.
func (uc *NextStoryUseCase) Execute(progressPath string, group *int) (string, error) {
	progress, err := uc.store.Load(progressPath)
	if err != nil {
		return "", fmt.Errorf("loading progress file %q: %w", progressPath, err)
	}

	eligible := progress.EligibleStories(group)
	if len(eligible) == 0 {
		return "", fmt.Errorf("next-story: %w", domain.ErrNoEligibleStory)
	}

	sort.Slice(eligible, func(i, j int) bool {
		ki, kj := eligible[i].SortKey(), eligible[j].SortKey()
		for idx := 0; idx < len(ki) && idx < len(kj); idx++ {
			if ki[idx] != kj[idx] {
				return ki[idx] < kj[idx]
			}
		}
		return len(ki) < len(kj)
	})

	next := eligible[0]
	docsRoot := progress.DocsFolder
	fullPath := filepath.Join(docsRoot, next.File)

	uc.log.Info("next eligible story", zap.String("story_id", next.ID), zap.String("path", fullPath))
	return fullPath, nil
}
