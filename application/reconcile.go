package application

import (
	"fmt"
	"time"

	"go.uber.org/zap"

	"github.com/sosalejandro/bmad-story-runner-cli/application/ports"
	"github.com/sosalejandro/bmad-story-runner-cli/domain"
)

type ReconcileUseCase struct {
	gateReader ports.GateReader
	store      ports.ProgressStore
	log        *zap.Logger
}

func NewReconcileUseCase(gateReader ports.GateReader, store ports.ProgressStore, log *zap.Logger) *ReconcileUseCase {
	return &ReconcileUseCase{gateReader: gateReader, store: store, log: log}
}

type ReconcileResult struct {
	Completed []string
	Blocked   []string
}

// Execute reads all gate files and updates progress JSON:
// - PASS -> status=complete, ci_passed=true, completed_at=now
// - FAIL/CONCERNS -> keep qa-review, append concerns to qa_concerns
// Only processes stories currently at qa-review status.
func (uc *ReconcileUseCase) Execute(progressPath string) (*ReconcileResult, error) {
	progress, err := uc.store.Load(progressPath)
	if err != nil {
		return nil, fmt.Errorf("loading progress file %q: %w", progressPath, err)
	}

	gates, err := uc.gateReader.ReadGates(progress.DocsFolder)
	if err != nil {
		return nil, fmt.Errorf("reading gate files from %q: %w", progress.DocsFolder, err)
	}

	// Build gate map for O(1) lookup.
	gateMap := make(map[string]*domain.StoryGate, len(gates))
	for _, g := range gates {
		gateMap[g.StoryID] = g
	}

	result := &ReconcileResult{}
	now := time.Now().UTC()

	for _, story := range progress.Stories {
		if story.Status != domain.StatusQAReview {
			continue
		}

		gate, ok := gateMap[story.ID]
		if !ok {
			uc.log.Warn("no gate file found for qa-review story", zap.String("story_id", story.ID))
			continue
		}

		switch gate.Result {
		case domain.GatePass:
			story.Status = domain.StatusComplete
			story.CIPassed = true
			story.CompletedAt = &now
			result.Completed = append(result.Completed, story.ID)
			uc.log.Info("reconciled story as complete", zap.String("story_id", story.ID))

		case domain.GateFail, domain.GateConcerns:
			if len(gate.Concerns) > 0 {
				story.QAConcerns = append(story.QAConcerns, gate.Concerns...)
			}
			result.Blocked = append(result.Blocked, story.ID)
			uc.log.Warn("story has gate concerns, keeping qa-review",
				zap.String("story_id", story.ID),
				zap.String("result", string(gate.Result)),
			)
		}
	}

	if err := uc.store.Save(progressPath, progress); err != nil {
		return nil, fmt.Errorf("saving reconciled progress to %q: %w", progressPath, err)
	}

	return result, nil
}
