package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

// newSystemCheckCmd reports current host resources + a max_safe_parallel
// heuristic. Linux-only via /proc; other OSes get a best-effort response.
func newSystemCheckCmd() *cobra.Command {
	var reserveMB int
	cmd := &cobra.Command{
		Use:   "system-check",
		Short: "Report free RAM / CPU load / max_safe_parallel for sprint planning",
		RunE: func(c *cobra.Command, args []string) error {
			report, err := buildSystemReport(reserveMB)
			if err != nil {
				return err
			}
			return json.NewEncoder(os.Stdout).Encode(report)
		},
	}
	cmd.Flags().IntVar(&reserveMB, "reserve", 0,
		"RAM (MB) to hold back from parallel budget (default: use config.reserve_ram_mb)")
	return cmd
}

type systemReport struct {
	OS               string  `json:"os"`
	CPUs             int     `json:"cpus"`
	FreeRamMB        int64   `json:"free_ram_mb"`
	TotalRamMB       int64   `json:"total_ram_mb"`
	LoadAvg1         float64 `json:"load_avg_1"`
	LoadAvg5         float64 `json:"load_avg_5"`
	LoadAvg15        float64 `json:"load_avg_15"`
	ReserveRamMB     int     `json:"reserve_ram_mb"`
	MaxSafeParallel  int     `json:"max_safe_parallel"`
	Notes            []string `json:"notes,omitempty"`
}

func buildSystemReport(reserveMB int) (systemReport, error) {
	rep := systemReport{
		OS:           runtime.GOOS,
		CPUs:         runtime.NumCPU(),
		ReserveRamMB: reserveMB,
	}

	if runtime.GOOS != "linux" {
		rep.Notes = append(rep.Notes,
			"non-linux: ram/load probing unavailable; max_safe_parallel defaulted from cpu count")
		rep.MaxSafeParallel = clampParallel(rep.CPUs / 2)
		return rep, nil
	}

	if total, free, err := readMemInfo(); err == nil {
		rep.TotalRamMB = total
		rep.FreeRamMB = free
	} else {
		rep.Notes = append(rep.Notes, "meminfo unavailable: "+err.Error())
	}

	if l1, l5, l15, err := readLoadAvg(); err == nil {
		rep.LoadAvg1, rep.LoadAvg5, rep.LoadAvg15 = l1, l5, l15
	} else {
		rep.Notes = append(rep.Notes, "loadavg unavailable: "+err.Error())
	}

	// Heuristic: max_safe_parallel = min of CPU-quarter, RAM-budget-quarter.
	// Assume ~800MB per parallel story (typical go workload per the §3 budget).
	const perStoryMB = 800
	budget := rep.FreeRamMB - int64(reserveMB)
	if budget < 0 {
		budget = 0
	}
	ramSlots := int(budget / perStoryMB)
	cpuSlots := rep.CPUs / 2
	rep.MaxSafeParallel = clampParallel(min(ramSlots, cpuSlots))
	return rep, nil
}

func clampParallel(n int) int {
	if n < 1 {
		return 1
	}
	if n > 16 {
		return 16
	}
	return n
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// readMemInfo returns (totalMB, freeMB) from /proc/meminfo. "Free" uses
// MemAvailable (the kernel's reclaimable-aware free) rather than MemFree.
func readMemInfo() (int64, int64, error) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()

	var total, available int64
	scan := bufio.NewScanner(f)
	for scan.Scan() {
		line := scan.Text()
		switch {
		case strings.HasPrefix(line, "MemTotal:"):
			total = parseMemLine(line)
		case strings.HasPrefix(line, "MemAvailable:"):
			available = parseMemLine(line)
		}
		if total > 0 && available > 0 {
			break
		}
	}
	if err := scan.Err(); err != nil {
		return 0, 0, err
	}
	return total / 1024, available / 1024, nil // kB → MB
}

func parseMemLine(line string) int64 {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return 0
	}
	n, _ := strconv.ParseInt(fields[1], 10, 64)
	return n
}

// readLoadAvg returns the three load averages from /proc/loadavg.
func readLoadAvg() (float64, float64, float64, error) {
	raw, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0, 0, 0, err
	}
	fields := strings.Fields(string(raw))
	if len(fields) < 3 {
		return 0, 0, 0, fmt.Errorf("loadavg malformed: %q", raw)
	}
	l1, _ := strconv.ParseFloat(fields[0], 64)
	l5, _ := strconv.ParseFloat(fields[1], 64)
	l15, _ := strconv.ParseFloat(fields[2], 64)
	return l1, l5, l15, nil
}
