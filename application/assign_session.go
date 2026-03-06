package application

import (
	"fmt"

	"go.uber.org/zap"

	"github.com/sosalejandro/bmad-story-runner-cli/application/ports"
)

type AssignSessionUseCase struct {
	store ports.ProgressStore
	log   *zap.Logger
}

func NewAssignSessionUseCase(store ports.ProgressStore, log *zap.Logger) *AssignSessionUseCase {
	return &AssignSessionUseCase{store: store, log: log}
}

func (uc *AssignSessionUseCase) Execute(progressPath string, group int, sessionID string) (int, error) {
	progress, err := uc.store.Load(progressPath)
	if err != nil {
		return 0, fmt.Errorf("loading progress file %q: %w", progressPath, err)
	}

	count := 0
	for _, s := range progress.Stories {
		if s.ParallelGroup != nil && *s.ParallelGroup == group && s.AssignedSession == nil {
			id := sessionID
			s.AssignedSession = &id
			count++
		}
	}

	uc.log.Info("assigned session to group",
		zap.String("session", sessionID),
		zap.Int("group", group),
		zap.Int("stories_assigned", count),
	)

	if err := uc.store.Save(progressPath, progress); err != nil {
		return 0, fmt.Errorf("saving progress file %q: %w", progressPath, err)
	}

	return count, nil
}
