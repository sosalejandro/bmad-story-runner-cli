package ports

import "github.com/sosalejandro/bmad-story-runner-cli/domain"

// GateReader reads QA gate files from the docs folder.
type GateReader interface {
	ReadGates(docsFolder string) ([]*domain.StoryGate, error)
}
