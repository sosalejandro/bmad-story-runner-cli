package infrastructure

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sosalejandro/bmad-story-runner-cli/domain"
)

func TestJSONLLogWriter_WriteAndRead(t *testing.T) {
	dir := t.TempDir()
	projectLog := filepath.Join(dir, "project", "bmad-audit.jsonl")
	globalLog := filepath.Join(dir, "global", "audit.jsonl")

	w := NewJSONLLogWriter(projectLog, globalLog)

	entry := &domain.LogEntry{
		Type:      domain.EntryTypeCommand,
		Version:   "1.0.0",
		Timestamp: time.Now().UTC(),
		Session:   domain.SessionInfo{PWD: "/test", PID: 42},
		Command:   &domain.CommandInfo{Name: "next", Args: []string{"p.json"}},
	}

	if err := w.WriteEntry(entry); err != nil {
		t.Fatalf("WriteEntry: %v", err)
	}

	// Verify project log exists and has one line.
	data, err := os.ReadFile(projectLog)
	if err != nil {
		t.Fatalf("reading project log: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}

	var decoded domain.LogEntry
	if err := json.Unmarshal([]byte(lines[0]), &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.Command.Name != "next" {
		t.Errorf("Command.Name = %q, want %q", decoded.Command.Name, "next")
	}

	// Verify global log also has the entry.
	globalData, err := os.ReadFile(globalLog)
	if err != nil {
		t.Fatalf("reading global log: %v", err)
	}
	if len(strings.TrimSpace(string(globalData))) == 0 {
		t.Error("global log should not be empty")
	}
}

func TestJSONLLogWriter_LastEntry(t *testing.T) {
	dir := t.TempDir()
	projectLog := filepath.Join(dir, "bmad-audit.jsonl")
	globalLog := filepath.Join(dir, "global", "audit.jsonl")

	w := NewJSONLLogWriter(projectLog, globalLog)

	// No entries yet.
	if last := w.LastEntry(); last != nil {
		t.Error("LastEntry should be nil for empty log")
	}

	// Write two entries.
	e1 := &domain.LogEntry{
		Type:    domain.EntryTypeCommand,
		Version: "1.0.0",
		Session: domain.SessionInfo{PWD: "/a", PID: 1},
		Command: &domain.CommandInfo{Name: "init"},
	}
	e2 := &domain.LogEntry{
		Type:    domain.EntryTypeCommand,
		Version: "1.0.0",
		Session: domain.SessionInfo{PWD: "/a", PID: 1},
		Command: &domain.CommandInfo{Name: "next"},
	}
	w.WriteEntry(e1)
	w.WriteEntry(e2)

	last := w.LastEntry()
	if last == nil {
		t.Fatal("LastEntry should not be nil")
	}
	if last.Command.Name != "next" {
		t.Errorf("LastEntry command = %q, want %q", last.Command.Name, "next")
	}
}

func TestJSONLLogWriter_Rotate(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "bmad-audit.jsonl")
	globalLog := filepath.Join(dir, "global", "audit.jsonl")

	w := NewJSONLLogWriter(logPath, globalLog)

	entry := &domain.LogEntry{
		Type:    domain.EntryTypeCommand,
		Version: "1.0.0",
		Session: domain.SessionInfo{PWD: "/test", PID: 1},
	}
	w.WriteEntry(entry)

	if err := w.Rotate(logPath); err != nil {
		t.Fatalf("Rotate: %v", err)
	}

	// Original file should be empty or gone, archive should exist.
	matches, _ := filepath.Glob(filepath.Join(dir, "audit-*.jsonl.gz"))
	if len(matches) == 0 {
		t.Error("no archive file found after rotation")
	}

	// Fresh log should be empty.
	data, _ := os.ReadFile(logPath)
	if len(data) != 0 {
		t.Error("log file should be empty after rotation")
	}
}

func TestJSONLLogWriter_ReadEntries(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "bmad-audit.jsonl")
	globalLog := filepath.Join(dir, "global", "audit.jsonl")

	w := NewJSONLLogWriter(logPath, globalLog)

	for i := 0; i < 3; i++ {
		w.WriteEntry(&domain.LogEntry{
			Type:    domain.EntryTypeCommand,
			Version: "1.0.0",
			Session: domain.SessionInfo{PWD: "/test", PID: 1},
			Command: &domain.CommandInfo{Name: "status"},
		})
	}

	entries, err := w.ReadEntries(logPath)
	if err != nil {
		t.Fatalf("ReadEntries: %v", err)
	}
	if len(entries) != 3 {
		t.Errorf("got %d entries, want 3", len(entries))
	}
}
