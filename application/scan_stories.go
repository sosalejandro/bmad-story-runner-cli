package application

import (
	"fmt"

	"go.uber.org/zap"

	"github.com/sosalejandro/bmad-story-runner-cli/application/ports"
)

type ScanStoriesUseCase struct {
	reporter ports.StoryScanReporter
	log      *zap.Logger
}

func NewScanStoriesUseCase(reporter ports.StoryScanReporter, log *zap.Logger) *ScanStoriesUseCase {
	return &ScanStoriesUseCase{reporter: reporter, log: log}
}

func (uc *ScanStoriesUseCase) Execute(docsFolder string) ([]*ports.StoryScanResult, error) {
	results, err := uc.reporter.Report(docsFolder)
	if err != nil {
		return nil, fmt.Errorf("scanning stories in %q: %w", docsFolder, err)
	}

	uc.log.Info("story scan complete",
		zap.String("docs_folder", docsFolder),
		zap.Int("stories", len(results)),
	)

	return results, nil
}
