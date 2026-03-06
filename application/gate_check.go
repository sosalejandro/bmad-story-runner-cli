package application

import (
	"fmt"

	"go.uber.org/zap"

	"github.com/sosalejandro/bmad-story-runner-cli/application/ports"
	"github.com/sosalejandro/bmad-story-runner-cli/domain"
)

type GateCheckUseCase struct {
	gateReader  ports.GateReader
	store       ports.ProgressStore
	log         *zap.Logger
}

func NewGateCheckUseCase(gateReader ports.GateReader, store ports.ProgressStore, log *zap.Logger) *GateCheckUseCase {
	return &GateCheckUseCase{gateReader: gateReader, store: store, log: log}
}

type GateCheckResult struct {
	Gates    []*domain.StoryGate
	HasFails bool
}

func (uc *GateCheckUseCase) Execute(progressPath string) (*GateCheckResult, error) {
	progress, err := uc.store.Load(progressPath)
	if err != nil {
		return nil, fmt.Errorf("loading progress file %q: %w", progressPath, err)
	}

	gates, err := uc.gateReader.ReadGates(progress.DocsFolder)
	if err != nil {
		return nil, fmt.Errorf("reading gate files from %q: %w", progress.DocsFolder, err)
	}

	var hasFails bool
	for _, g := range gates {
		uc.log.Info("gate result",
			zap.String("story_id", g.StoryID),
			zap.String("result", string(g.Result)),
		)
		if g.Result.IsBlocking() {
			hasFails = true
		}
	}

	return &GateCheckResult{Gates: gates, HasFails: hasFails}, nil
}
