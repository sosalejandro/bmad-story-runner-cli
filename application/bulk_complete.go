package application

import (
	"fmt"
	"time"

	"go.uber.org/zap"

	"github.com/sosalejandro/bmad-story-runner-cli/application/ports"
	"github.com/sosalejandro/bmad-story-runner-cli/domain"
)

type BulkCompleteUseCase struct {
	store ports.ProgressStore
	log   *zap.Logger
}

func NewBulkCompleteUseCase(store ports.ProgressStore, log *zap.Logger) *BulkCompleteUseCase {
	return &BulkCompleteUseCase{store: store, log: log}
}

type BulkCompleteResult struct {
	Completed []string
	NotFound  []string
}

// Execute atomically marks multiple stories complete in a single JSON write.
// Only call this after CI has passed for all listed stories.
func (uc *BulkCompleteUseCase) Execute(progressPath string, storyIDs []string) (*BulkCompleteResult, error) {
	if len(storyIDs) == 0 {
		return nil, fmt.Errorf("bulk-complete: no story IDs provided")
	}

	progress, err := uc.store.Load(progressPath)
	if err != nil {
		return nil, fmt.Errorf("loading progress file %q: %w", progressPath, err)
	}

	now := time.Now().UTC()
	idSet := make(map[string]bool, len(storyIDs))
	for _, id := range storyIDs {
		idSet[id] = true
	}

	result := &BulkCompleteResult{}
	matched := make(map[string]bool)

	for _, story := range progress.Stories {
		if !idSet[story.ID] {
			continue
		}
		story.Status = domain.StatusComplete
		story.CIPassed = true
		story.CompletedAt = &now
		matched[story.ID] = true
		result.Completed = append(result.Completed, story.ID)
		uc.log.Info("bulk marking story complete", zap.String("story_id", story.ID), zap.Time("completed_at", now))
	}

	// Identify any IDs that were not found in the progress file.
	for _, id := range storyIDs {
		if !matched[id] {
			result.NotFound = append(result.NotFound, id)
			uc.log.Warn("story ID not found in progress file", zap.String("story_id", id))
		}
	}

	if len(result.NotFound) > 0 {
		return result, fmt.Errorf("bulk-complete: %w: %v",
			domain.Join(func() []error {
				errs := make([]error, len(result.NotFound))
				for i, id := range result.NotFound {
					errs[i] = &domain.StoryNotFoundError{ID: id}
				}
				return errs
			}()...),
			result.NotFound,
		)
	}

	if err := uc.store.Save(progressPath, progress); err != nil {
		return nil, fmt.Errorf("saving progress file %q: %w", progressPath, err)
	}

	return result, nil
}
