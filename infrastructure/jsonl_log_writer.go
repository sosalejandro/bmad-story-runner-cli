package infrastructure

import (
	"bufio"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/sosalejandro/bmad-story-runner-cli/domain"
)

// JSONLLogWriter writes audit log entries to JSONL files.
// It dual-writes to both a project-specific log and a global log.
type JSONLLogWriter struct {
	projectLogPath string
	globalLogPath  string
}

// NewJSONLLogWriter creates a log writer that writes to both paths.
func NewJSONLLogWriter(projectLogPath, globalLogPath string) *JSONLLogWriter {
	return &JSONLLogWriter{
		projectLogPath: projectLogPath,
		globalLogPath:  globalLogPath,
	}
}

// WriteEntry appends a JSON-encoded log entry to both log files.
func (w *JSONLLogWriter) WriteEntry(entry *domain.LogEntry) error {
	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshalling log entry: %w", err)
	}
	data = append(data, '\n')

	if err := w.appendToFile(w.projectLogPath, data); err != nil {
		return fmt.Errorf("writing project log: %w", err)
	}
	if err := w.appendToFile(w.globalLogPath, data); err != nil {
		// Log to project succeeded — don't fail the whole operation
		// for a global log write failure. Best-effort.
		return nil
	}
	return nil
}

// LastEntry reads the most recent entry from the project log.
func (w *JSONLLogWriter) LastEntry() *domain.LogEntry {
	entries, err := w.ReadEntries(w.projectLogPath)
	if err != nil || len(entries) == 0 {
		return nil
	}
	return entries[len(entries)-1]
}

// ReadEntries reads all entries from the specified JSONL file.
func (w *JSONLLogWriter) ReadEntries(path string) ([]*domain.LogEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("opening log file: %w", err)
	}
	defer f.Close()

	var entries []*domain.LogEntry
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024) // 1MB max line
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var entry domain.LogEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			continue // skip malformed lines
		}
		entries = append(entries, &entry)
	}
	return entries, scanner.Err()
}

// Rotate archives the log file to a gzipped timestamped file and creates a fresh empty log.
func (w *JSONLLogWriter) Rotate(path string) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil // nothing to rotate
	}

	dir := filepath.Dir(path)
	archiveName := fmt.Sprintf("audit-%s.jsonl.gz", time.Now().UTC().Format("2006-01-02"))
	archivePath := filepath.Join(dir, archiveName)

	// Read current log.
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading log for rotation: %w", err)
	}

	// Write gzipped archive.
	af, err := os.Create(archivePath)
	if err != nil {
		return fmt.Errorf("creating archive: %w", err)
	}
	gz := gzip.NewWriter(af)
	if _, err := gz.Write(data); err != nil {
		gz.Close()
		af.Close()
		return fmt.Errorf("writing archive: %w", err)
	}
	gz.Close()
	af.Close()

	// Truncate the original log.
	return os.WriteFile(path, nil, 0644)
}

// appendToFile creates parent directories and appends data to a file.
func (w *JSONLLogWriter) appendToFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(data)
	return err
}
