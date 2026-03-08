package application

import (
	"testing"

	"github.com/sosalejandro/bmad-story-runner-cli/domain"
)

func TestComputeStats_BasicCounts(t *testing.T) {
	entries := []*domain.LogEntry{
		{Type: domain.EntryTypeSessionStart, Session: domain.SessionInfo{PWD: "/a", PID: 1}},
		{Type: domain.EntryTypeCommand, Command: &domain.CommandInfo{Name: "next"}, Performance: &domain.PerformanceStats{TotalMs: 10}, Result: &domain.ResultInfo{ExitCode: 0}},
		{Type: domain.EntryTypeCommand, Command: &domain.CommandInfo{Name: "next"}, Performance: &domain.PerformanceStats{TotalMs: 20}, Result: &domain.ResultInfo{ExitCode: 0}},
		{Type: domain.EntryTypeCommand, Command: &domain.CommandInfo{Name: "status"}, Performance: &domain.PerformanceStats{TotalMs: 15}, Result: &domain.ResultInfo{ExitCode: 0}},
		{Type: domain.EntryTypeExec, Command: &domain.CommandInfo{Name: "exec"}, Exec: &domain.ExecInfo{ChildDurationMs: 1000, Context: domain.ExecContext{Reason: "json manipulation"}}, Result: &domain.ResultInfo{ExitCode: 0}},
	}

	report := ComputeStats(entries)

	if report.TotalCommands != 3 {
		t.Errorf("TotalCommands = %d, want 3", report.TotalCommands)
	}
	if report.TotalExecs != 1 {
		t.Errorf("TotalExecs = %d, want 1", report.TotalExecs)
	}
	if report.Sessions != 1 {
		t.Errorf("Sessions = %d, want 1", report.Sessions)
	}
	if len(report.ExecReasons) != 1 {
		t.Errorf("ExecReasons count = %d, want 1", len(report.ExecReasons))
	}
}

func TestComputeStats_Percentiles(t *testing.T) {
	var entries []*domain.LogEntry
	durations := []float64{5, 10, 15, 20, 25, 30, 35, 40, 45, 50}
	for _, d := range durations {
		entries = append(entries, &domain.LogEntry{
			Type:        domain.EntryTypeCommand,
			Command:     &domain.CommandInfo{Name: "next"},
			Performance: &domain.PerformanceStats{TotalMs: d},
			Result:      &domain.ResultInfo{ExitCode: 0},
		})
	}

	report := ComputeStats(entries)
	var nextStats *CommandStats
	for i := range report.Commands {
		if report.Commands[i].Name == "next" {
			nextStats = &report.Commands[i]
			break
		}
	}
	if nextStats == nil {
		t.Fatal("no stats for 'next' command")
	}
	if nextStats.AvgMs != 27.5 {
		t.Errorf("AvgMs = %f, want 27.5", nextStats.AvgMs)
	}
	if nextStats.P50Ms != 25.0 {
		t.Errorf("P50Ms = %f, want 25.0", nextStats.P50Ms)
	}
}

func TestPercentile_EdgeCases(t *testing.T) {
	if p := percentile(nil, 50); p != 0 {
		t.Errorf("nil slice: got %f, want 0", p)
	}
	if p := percentile([]float64{42}, 99); p != 42 {
		t.Errorf("single element: got %f, want 42", p)
	}
}
