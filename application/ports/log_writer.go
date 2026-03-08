package ports

import "github.com/sosalejandro/bmad-story-runner-cli/domain"

// LogWriter handles audit log persistence.
type LogWriter interface {
	// WriteEntry appends a log entry to both project and global logs.
	WriteEntry(entry *domain.LogEntry) error

	// LastEntry reads the most recent entry from the project log.
	// Returns nil if the log is empty or does not exist.
	LastEntry() *domain.LogEntry

	// ReadEntries reads all entries from the specified log file.
	ReadEntries(path string) ([]*domain.LogEntry, error)

	// Rotate archives the current log to a gzipped timestamped file.
	Rotate(path string) error
}
