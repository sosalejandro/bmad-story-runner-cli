package application

import (
	"math"
	"sort"

	"github.com/sosalejandro/bmad-story-runner-cli/domain"
)

// CommandStats holds aggregated metrics for a single command name.
type CommandStats struct {
	Name         string
	Count        int
	AvgMs        float64
	P50Ms        float64
	P95Ms        float64
	P99Ms        float64
	AvgHeapKB    float64
	MaxPeakRSSMB float64
	Errors       int
}

// ExecReasonStats holds aggregated metrics for a single exec reason.
type ExecReasonStats struct {
	Reason     string
	Count      int
	AvgMs      float64
	AvgRSSMB   float64
	TotalTimeS float64
}

// StatsReport is the output of log stats aggregation.
type StatsReport struct {
	Commands      []CommandStats
	ExecReasons   []ExecReasonStats
	TotalCommands int
	TotalExecs    int
	TotalErrors   int
	Sessions      int
}

// ComputeStats aggregates log entries into a stats report.
func ComputeStats(entries []*domain.LogEntry) *StatsReport {
	report := &StatsReport{}

	// Group entries by command name.
	cmdEntries := make(map[string][]*domain.LogEntry)
	reasonEntries := make(map[string][]*domain.LogEntry)
	sessionKeys := make(map[string]bool)

	for _, e := range entries {
		if e.Type == domain.EntryTypeSessionStart {
			key := e.Session.PWD + "|" + string(rune(e.Session.PID))
			sessionKeys[key] = true
			continue
		}
		if e.Command == nil {
			continue
		}

		name := e.Command.Name
		cmdEntries[name] = append(cmdEntries[name], e)

		if e.Type == domain.EntryTypeCommand {
			report.TotalCommands++
		}
		if e.Type == domain.EntryTypeExec {
			report.TotalExecs++
			if e.Exec != nil && e.Exec.Context.Reason != "" {
				reasonEntries[e.Exec.Context.Reason] = append(reasonEntries[e.Exec.Context.Reason], e)
			}
		}
		if e.Result != nil && e.Result.ExitCode != 0 {
			report.TotalErrors++
		}
	}

	report.Sessions = len(sessionKeys)

	// Compute per-command stats.
	for name, entries := range cmdEntries {
		cs := CommandStats{Name: name, Count: len(entries)}
		var durations []float64
		var heapSum float64
		var maxRSS float64

		for _, e := range entries {
			if e.Performance != nil {
				durations = append(durations, e.Performance.TotalMs)
			}
			if e.Memory != nil {
				heapSum += float64(e.Memory.HeapAllocBytes) / 1024
				rssMB := float64(e.Memory.PeakRSSBytes) / (1024 * 1024)
				if rssMB > maxRSS {
					maxRSS = rssMB
				}
			}
			if e.Result != nil && e.Result.ExitCode != 0 {
				cs.Errors++
			}
		}

		if len(durations) > 0 {
			sort.Float64s(durations)
			cs.AvgMs = avg(durations)
			cs.P50Ms = percentile(durations, 50)
			cs.P95Ms = percentile(durations, 95)
			cs.P99Ms = percentile(durations, 99)
		}
		cs.AvgHeapKB = heapSum / float64(len(entries))
		cs.MaxPeakRSSMB = maxRSS

		report.Commands = append(report.Commands, cs)
	}

	// Sort commands by count descending.
	sort.Slice(report.Commands, func(i, j int) bool {
		return report.Commands[i].Count > report.Commands[j].Count
	})

	// Compute per-reason stats.
	for reason, entries := range reasonEntries {
		rs := ExecReasonStats{Reason: reason, Count: len(entries)}
		var totalMs float64
		var totalRSS float64

		for _, e := range entries {
			if e.Exec != nil {
				totalMs += e.Exec.ChildDurationMs
				totalRSS += float64(e.Exec.ChildPeakRSSBytes) / (1024 * 1024)
			}
		}

		rs.AvgMs = totalMs / float64(len(entries))
		rs.AvgRSSMB = totalRSS / float64(len(entries))
		rs.TotalTimeS = totalMs / 1000

		report.ExecReasons = append(report.ExecReasons, rs)
	}

	// Sort reasons by count descending.
	sort.Slice(report.ExecReasons, func(i, j int) bool {
		return report.ExecReasons[i].Count > report.ExecReasons[j].Count
	})

	return report
}

func avg(vals []float64) float64 {
	sum := 0.0
	for _, v := range vals {
		sum += v
	}
	return sum / float64(len(vals))
}

func percentile(sorted []float64, pct float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(math.Ceil(pct/100*float64(len(sorted)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}
