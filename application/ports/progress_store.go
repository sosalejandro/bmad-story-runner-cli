package ports

import "github.com/sosalejandro/bmad-story-runner-cli/domain"

// ProgressStore handles persistence of the bmad-progress.json file.
type ProgressStore interface {
	Load(path string) (*domain.ProgressFile, error)
	Save(path string, progress *domain.ProgressFile) error
}
