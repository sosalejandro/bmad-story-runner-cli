package ports

import "github.com/sosalejandro/bmad-story-runner-cli/domain"

// StoryScanner discovers and parses story .md files from a docs folder.
type StoryScanner interface {
	Scan(docsFolder string) ([]*domain.Story, error)
}

// StoryFilePatcher updates the **Status:** line in a story .md file.
type StoryFilePatcher interface {
	PatchStatus(filePath string, status string) error
}

// QAPendingScanner finds story files that still contain placeholder QA sections.
type QAPendingScanner interface {
	FindPending(docsFolder string) ([]string, error)
}

// StoryScanResult holds task completion stats for a story file.
type StoryScanResult struct {
	StoryID      string
	File         string
	Title        string
	ACCount      int
	TasksDone    int
	TasksTotal   int
}

// StoryScanReporter reads story files and returns task completion counts.
type StoryScanReporter interface {
	Report(docsFolder string) ([]*StoryScanResult, error)
}
