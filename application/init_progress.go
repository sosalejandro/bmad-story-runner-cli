package application

import (
	"fmt"
	"time"

	"go.uber.org/zap"

	"github.com/sosalejandro/bmad-story-runner-cli/application/ports"
	"github.com/sosalejandro/bmad-story-runner-cli/domain"
)

type InitProgressUseCase struct {
	scanner ports.StoryScanner
	store   ports.ProgressStore
	log     *zap.Logger
}

func NewInitProgressUseCase(scanner ports.StoryScanner, store ports.ProgressStore, log *zap.Logger) *InitProgressUseCase {
	return &InitProgressUseCase{scanner: scanner, store: store, log: log}
}

type InitProgressResult struct {
	StoriesFound      int
	FlaggedAsComplete []string
}

func (uc *InitProgressUseCase) Execute(docsFolder, progressPath string) (*InitProgressResult, error) {
	uc.log.Info("initializing progress file", zap.String("docs_folder", docsFolder), zap.String("path", progressPath))

	stories, err := uc.scanner.Scan(docsFolder)
	if err != nil {
		return nil, fmt.Errorf("scanning stories in %q: %w", docsFolder, err)
	}

	var flagged []string
	for _, s := range stories {
		if s.Status == domain.StatusComplete {
			s.CIPassed = false
			flagged = append(flagged, s.ID)
			uc.log.Warn("story appears complete but ci_passed unverified", zap.String("story_id", s.ID))
		}
	}

	progress := &domain.ProgressFile{
		Version:     1,
		DocsFolder:  docsFolder,
		LastUpdated: time.Now().UTC(),
		Stories:     stories,
	}

	if err := uc.store.Save(progressPath, progress); err != nil {
		return nil, fmt.Errorf("saving progress file to %q: %w", progressPath, err)
	}

	uc.log.Info("progress file created", zap.Int("stories", len(stories)), zap.Int("flagged", len(flagged)))
	return &InitProgressResult{StoriesFound: len(stories), FlaggedAsComplete: flagged}, nil
}
