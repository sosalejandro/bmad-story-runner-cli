package application

import (
	"errors"
	"fmt"

	"go.uber.org/zap"

	"github.com/sosalejandro/bmad-story-runner-cli/application/ports"
	"github.com/sosalejandro/bmad-story-runner-cli/domain"
)

type SetStatusUseCase struct {
	store ports.ProgressStore
	log   *zap.Logger
}

func NewSetStatusUseCase(store ports.ProgressStore, log *zap.Logger) *SetStatusUseCase {
	return &SetStatusUseCase{store: store, log: log}
}

func (uc *SetStatusUseCase) Execute(progressPath, storyID, rawStatus string) error {
	status, err := domain.ParseStatus(rawStatus)
	if err != nil {
		if errors.Is(err, domain.ErrInvalidStatus) {
			return fmt.Errorf("set-status validation: %w", err)
		}
		return fmt.Errorf("parsing status: %w", err)
	}

	progress, err := uc.store.Load(progressPath)
	if err != nil {
		return fmt.Errorf("loading progress file %q: %w", progressPath, err)
	}

	story := progress.FindByID(storyID)
	if story == nil {
		return fmt.Errorf("set-status: %w", &domain.StoryNotFoundError{ID: storyID})
	}

	prev := story.Status
	story.Status = status
	uc.log.Info("updating story status",
		zap.String("story_id", storyID),
		zap.String("from", string(prev)),
		zap.String("to", string(status)),
	)

	if err := uc.store.Save(progressPath, progress); err != nil {
		return fmt.Errorf("saving progress file %q: %w", progressPath, err)
	}

	return nil
}
