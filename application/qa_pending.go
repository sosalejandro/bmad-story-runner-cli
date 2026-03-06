package application

import (
	"fmt"

	"go.uber.org/zap"

	"github.com/sosalejandro/bmad-story-runner-cli/application/ports"
)

type QAPendingUseCase struct {
	scanner ports.QAPendingScanner
	log     *zap.Logger
}

func NewQAPendingUseCase(scanner ports.QAPendingScanner, log *zap.Logger) *QAPendingUseCase {
	return &QAPendingUseCase{scanner: scanner, log: log}
}

func (uc *QAPendingUseCase) Execute(docsFolder string) ([]string, error) {
	files, err := uc.scanner.FindPending(docsFolder)
	if err != nil {
		return nil, fmt.Errorf("scanning for pending QA in %q: %w", docsFolder, err)
	}

	uc.log.Info("qa-pending scan complete",
		zap.String("docs_folder", docsFolder),
		zap.Int("pending_count", len(files)),
	)

	return files, nil
}
