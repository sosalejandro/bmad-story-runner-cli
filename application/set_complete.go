package application

import (
	"fmt"
	"time"

	"go.uber.org/zap"

	"github.com/sosalejandro/bmad-story-runner-cli/application/ports"
	"github.com/sosalejandro/bmad-story-runner-cli/domain"
)

type SetCompleteUseCase struct {
	store ports.ProgressStore
	log   *zap.Logger
}

func NewSetCompleteUseCase(store ports.ProgressStore, log *zap.Logger) *SetCompleteUseCase {
	return &SetCompleteUseCase{store: store, log: log}
}

// Execute atomically marks a story complete after CI has passed.
// Only call this after CI passes — it sets status=complete, ci_passed=true, and completed_at=now.
func (uc *SetCompleteUseCase) Execute(progressPath, storyID string) error {
	progress, err := uc.store.Load(progressPath)
	if err != nil {
		return fmt.Errorf("loading progress file %q: %w", progressPath, err)
	}

	story := progress.FindByID(storyID)
	if story == nil {
		return fmt.Errorf("set-complete: %w", &domain.StoryNotFoundError{ID: storyID})
	}

	now := time.Now().UTC()
	story.Status = domain.StatusComplete
	story.CIPassed = true
	story.CompletedAt = &now

	uc.log.Info("marking story complete",
		zap.String("story_id", storyID),
		zap.Time("completed_at", now),
	)

	if err := uc.store.Save(progressPath, progress); err != nil {
		return fmt.Errorf("saving progress file %q: %w", progressPath, err)
	}

	return nil
}
