package application

import (
	"fmt"

	"go.uber.org/zap"

	"github.com/sosalejandro/bmad-story-runner-cli/application/ports"
	"github.com/sosalejandro/bmad-story-runner-cli/domain"
)

type AddConcernsUseCase struct {
	store ports.ProgressStore
	log   *zap.Logger
}

func NewAddConcernsUseCase(store ports.ProgressStore, log *zap.Logger) *AddConcernsUseCase {
	return &AddConcernsUseCase{store: store, log: log}
}

func (uc *AddConcernsUseCase) Execute(progressPath, storyID string, concerns []domain.QAConcern) error {
	if len(concerns) == 0 {
		return fmt.Errorf("add-concerns: no concerns provided for story %q", storyID)
	}

	progress, err := uc.store.Load(progressPath)
	if err != nil {
		return fmt.Errorf("loading progress file %q: %w", progressPath, err)
	}

	story := progress.FindByID(storyID)
	if story == nil {
		return fmt.Errorf("add-concerns: %w", &domain.StoryNotFoundError{ID: storyID})
	}

	story.QAConcerns = append(story.QAConcerns, concerns...)
	uc.log.Info("appended QA concerns",
		zap.String("story_id", storyID),
		zap.Int("count", len(concerns)),
	)

	if err := uc.store.Save(progressPath, progress); err != nil {
		return fmt.Errorf("saving progress file %q: %w", progressPath, err)
	}

	return nil
}
