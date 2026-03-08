package infrastructure

import (
	"bufio"
	"fmt"
	"os"
	"os/user"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/sosalejandro/bmad-story-runner-cli/domain"
)

// CollectMemoryStats reads Go runtime memory statistics and Linux peak RSS.
func CollectMemoryStats() domain.MemoryStats {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	return domain.MemoryStats{
		HeapAllocBytes:  m.HeapAlloc,
		HeapSysBytes:    m.HeapSys,
		TotalAllocBytes: m.TotalAlloc,
		NumGC:           m.NumGC,
		GCPauseTotalNs:  m.PauseTotalNs,
		GCPauseLastNs:   m.PauseNs[(m.NumGC+255)%256],
		Goroutines:      runtime.NumGoroutine(),
		StackInuseBytes: m.StackInuse,
		PeakRSSBytes:    readPeakRSS(),
		Mallocs:         m.Mallocs,
		Frees:           m.Frees,
	}
}

// CollectSessionInfo gathers environment metadata for the current process.
func CollectSessionInfo(version, commit string) domain.SessionInfo {
	pwd, _ := os.Getwd()
	hostname, _ := os.Hostname()
	username := ""
	if u, err := user.Current(); err == nil {
		username = u.Username
	}

	return domain.SessionInfo{
		PWD:         pwd,
		Terminal:    detectTerminal(),
		PID:         os.Getpid(),
		PPID:        os.Getppid(),
		User:        username,
		Hostname:    hostname,
		OS:          runtime.GOOS,
		Arch:        runtime.GOARCH,
		GoVersion:   runtime.Version(),
		BmadVersion: version,
		BmadCommit:  commit,
	}
}

// CollectChildMetrics extracts CPU time and peak RSS from a completed child process.
func CollectChildMetrics(state *os.ProcessState) (userTimeMs, sysTimeMs float64, peakRSSBytes uint64) {
	if state == nil {
		return 0, 0, 0
	}
	usage, ok := state.SysUsage().(*syscall.Rusage)
	if !ok || usage == nil {
		return 0, 0, 0
	}
	userTimeMs = float64(usage.Utime.Sec)*1000 + float64(usage.Utime.Usec)/1000
	sysTimeMs = float64(usage.Stime.Sec)*1000 + float64(usage.Stime.Usec)/1000
	peakRSSBytes = uint64(usage.Maxrss) * 1024 // Maxrss is in KB on Linux
	return
}

// readPeakRSS reads VmHWM from /proc/self/status (Linux only).
// Returns 0 on non-Linux platforms or on error.
func readPeakRSS() uint64 {
	if runtime.GOOS != "linux" {
		return 0
	}
	f, err := os.Open("/proc/self/status")
	if err != nil {
		return 0
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "VmHWM:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				kb, err := strconv.ParseUint(fields[1], 10, 64)
				if err == nil {
					return kb * 1024
				}
			}
		}
	}
	return 0
}

// detectTerminal tries to identify the parent process name.
func detectTerminal() string {
	ppid := os.Getppid()
	comm, err := os.ReadFile(fmt.Sprintf("/proc/%d/comm", ppid))
	if err != nil {
		return "unknown"
	}
	name := strings.TrimSpace(string(comm))
	// Map known parent process names to friendly labels.
	switch {
	case strings.Contains(name, "claude"):
		return "claude-code"
	case strings.Contains(name, "code"):
		return "vscode"
	default:
		return name
	}
}

// PhaseTimer tracks time spent in named execution phases.
type PhaseTimer struct {
	phases  map[string]int64 // phase name -> nanoseconds
	current string
	start   int64
}

// NewPhaseTimer creates a new phase timer.
func NewPhaseTimer() *PhaseTimer {
	return &PhaseTimer{
		phases: make(map[string]int64),
	}
}

// StartPhase begins timing a named phase. Ends the previous phase if one is active.
func (t *PhaseTimer) StartPhase(name string) {
	now := nanotime()
	if t.current != "" {
		t.phases[t.current] += now - t.start
	}
	t.current = name
	t.start = now
}

// EndPhase stops timing the current phase.
func (t *PhaseTimer) EndPhase() {
	if t.current == "" {
		return
	}
	now := nanotime()
	t.phases[t.current] += now - t.start
	t.current = ""
}

// ToPerformanceStats converts phase timings to a PerformanceStats struct.
func (t *PhaseTimer) ToPerformanceStats(wallClockNs int64) domain.PerformanceStats {
	t.EndPhase() // close any open phase
	ms := func(ns int64) float64 { return float64(ns) / 1e6 }
	ps := domain.PerformanceStats{
		IOReadMs:    ms(t.phases["io_read"]),
		IOWriteMs:   ms(t.phases["io_write"]),
		ParseMs:     ms(t.phases["parse"]),
		LogicMs:     ms(t.phases["logic"]),
		WallClockNs: wallClockNs,
	}
	ps.TotalMs = float64(wallClockNs) / 1e6
	return ps
}

func nanotime() int64 {
	return time.Now().UnixNano()
}
