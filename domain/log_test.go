package domain

import (
	"testing"
	"time"
)

func TestLogEntryTypeConstants(t *testing.T) {
	if EntryTypeCommand != "command" {
		t.Errorf("EntryTypeCommand = %q, want %q", EntryTypeCommand, "command")
	}
	if EntryTypeExec != "exec" {
		t.Errorf("EntryTypeExec = %q, want %q", EntryTypeExec, "exec")
	}
	if EntryTypeSessionStart != "session_start" {
		t.Errorf("EntryTypeSessionStart = %q, want %q", EntryTypeSessionStart, "session_start")
	}
}

func TestLogEntryIsNewSession(t *testing.T) {
	prev := &LogEntry{
		Session: SessionInfo{PWD: "/project-a", PID: 100},
	}
	same := &LogEntry{
		Session: SessionInfo{PWD: "/project-a", PID: 100},
	}
	diff := &LogEntry{
		Session: SessionInfo{PWD: "/project-b", PID: 200},
	}

	if prev.IsNewSession(same) {
		t.Error("same pwd+pid should not be a new session")
	}
	if !prev.IsNewSession(diff) {
		t.Error("different pwd+pid should be a new session")
	}
}

func TestPerformanceStatsTotalFromPhases(t *testing.T) {
	ps := PerformanceStats{
		IOReadMs:  4.0,
		IOWriteMs: 2.0,
		ParseMs:   3.0,
		LogicMs:   5.0,
	}
	ps.ComputeTotal()
	if ps.TotalMs != 14.0 {
		t.Errorf("TotalMs = %f, want 14.0", ps.TotalMs)
	}
}

func TestSessionInfoMatch(t *testing.T) {
	a := SessionInfo{PWD: "/a", PID: 1}
	b := SessionInfo{PWD: "/a", PID: 1}
	c := SessionInfo{PWD: "/b", PID: 2}

	if !a.Match(b) {
		t.Error("identical sessions should match")
	}
	if a.Match(c) {
		t.Error("different sessions should not match")
	}
}

func TestNewSessionEntry(t *testing.T) {
	info := SessionInfo{
		PWD:         "/test",
		PID:         42,
		BmadVersion: "0.4.0",
	}
	entry := NewSessionStartEntry(info)
	if entry.Type != EntryTypeSessionStart {
		t.Errorf("Type = %q, want %q", entry.Type, EntryTypeSessionStart)
	}
	if entry.Timestamp.IsZero() {
		t.Error("Timestamp should not be zero")
	}
	_ = time.Now() // reference import
}
