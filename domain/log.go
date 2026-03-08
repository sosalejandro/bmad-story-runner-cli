package domain

import "time"

// Entry type constants.
const (
	EntryTypeCommand      = "command"
	EntryTypeExec         = "exec"
	EntryTypeSessionStart = "session_start"
)

// LogEntry is a single audit log record.
type LogEntry struct {
	Type        string           `json:"type"`
	Version     string           `json:"version"`
	Timestamp   time.Time        `json:"timestamp"`
	Session     SessionInfo      `json:"session"`
	Command     *CommandInfo     `json:"command,omitempty"`
	Result      *ResultInfo      `json:"result,omitempty"`
	Performance *PerformanceStats `json:"performance,omitempty"`
	Memory      *MemoryStats     `json:"memory,omitempty"`
	IO          *IOStats         `json:"io,omitempty"`
	Exec        *ExecInfo        `json:"exec,omitempty"`
}

// SessionInfo identifies the terminal session and environment.
type SessionInfo struct {
	PWD         string `json:"pwd"`
	Terminal    string `json:"terminal"`
	PID         int    `json:"pid"`
	PPID        int    `json:"ppid"`
	User        string `json:"user"`
	Hostname    string `json:"hostname"`
	OS          string `json:"os"`
	Arch        string `json:"arch"`
	GoVersion   string `json:"go_version"`
	BmadVersion string `json:"bmad_version"`
	BmadCommit  string `json:"bmad_commit"`
}

// Match returns true if both sessions have the same PWD and PID.
func (s SessionInfo) Match(other SessionInfo) bool {
	return s.PWD == other.PWD && s.PID == other.PID
}

// CommandInfo describes the bmad command that was invoked.
type CommandInfo struct {
	Name  string            `json:"name"`
	Args  []string          `json:"args"`
	Raw   string            `json:"raw"`
	Flags map[string]string `json:"flags,omitempty"`
}

// ResultInfo captures command outcome.
type ResultInfo struct {
	ExitCode    int     `json:"exit_code"`
	StdoutBytes int64   `json:"stdout_bytes"`
	StderrBytes int64   `json:"stderr_bytes"`
	Error       *string `json:"error"`
	ErrorType   *string `json:"error_type"`
}

// PerformanceStats captures timing breakdown.
type PerformanceStats struct {
	TotalMs     float64 `json:"total_ms"`
	IOReadMs    float64 `json:"io_read_ms"`
	IOWriteMs   float64 `json:"io_write_ms"`
	ParseMs     float64 `json:"parse_ms"`
	LogicMs     float64 `json:"logic_ms"`
	WallClockNs int64   `json:"wall_clock_ns"`
}

// ComputeTotal sums the phase durations into TotalMs.
func (p *PerformanceStats) ComputeTotal() {
	p.TotalMs = p.IOReadMs + p.IOWriteMs + p.ParseMs + p.LogicMs
}

// MemoryStats captures Go runtime memory metrics.
type MemoryStats struct {
	HeapAllocBytes  uint64 `json:"heap_alloc_bytes"`
	HeapSysBytes    uint64 `json:"heap_sys_bytes"`
	TotalAllocBytes uint64 `json:"total_alloc_bytes"`
	NumGC           uint32 `json:"num_gc"`
	GCPauseTotalNs  uint64 `json:"gc_pause_total_ns"`
	GCPauseLastNs   uint64 `json:"gc_pause_last_ns"`
	Goroutines      int    `json:"goroutines"`
	StackInuseBytes uint64 `json:"stack_inuse_bytes"`
	PeakRSSBytes    uint64 `json:"peak_rss_bytes"`
	Mallocs         uint64 `json:"mallocs"`
	Frees           uint64 `json:"frees"`
}

// IOStats captures domain-level I/O counters.
type IOStats struct {
	FilesRead        int   `json:"files_read"`
	FilesWritten     int   `json:"files_written"`
	BytesRead        int64 `json:"bytes_read"`
	BytesWritten     int64 `json:"bytes_written"`
	StoriesProcessed int   `json:"stories_processed"`
}

// ExecInfo captures child process metrics for bmad exec.
type ExecInfo struct {
	ChildCommand      string      `json:"child_command"`
	ChildArgs         []string    `json:"child_args"`
	ChildExitCode     int         `json:"child_exit_code"`
	ChildDurationMs   float64     `json:"child_duration_ms"`
	ChildStdoutBytes  int64       `json:"child_stdout_bytes"`
	ChildStderrBytes  int64       `json:"child_stderr_bytes"`
	ChildPeakRSSBytes uint64      `json:"child_peak_rss_bytes"`
	ChildUserTimeMs   float64     `json:"child_user_time_ms"`
	ChildSysTimeMs    float64     `json:"child_sys_time_ms"`
	Context           ExecContext `json:"context"`
}

// ExecContext links exec calls to the preceding bmad command.
type ExecContext struct {
	PreviousBmadCommand string `json:"previous_bmad_command"`
	Reason              string `json:"reason"`
}

// IsNewSession returns true if this entry's session differs from prev.
func (e *LogEntry) IsNewSession(prev *LogEntry) bool {
	return !e.Session.Match(prev.Session)
}

// NewSessionStartEntry creates a session_start log entry.
func NewSessionStartEntry(session SessionInfo) *LogEntry {
	return &LogEntry{
		Type:      EntryTypeSessionStart,
		Version:   "1.0.0",
		Timestamp: time.Now().UTC(),
		Session:   session,
	}
}

// NewCommandEntry creates a command log entry.
func NewCommandEntry(session SessionInfo, cmd *CommandInfo) *LogEntry {
	return &LogEntry{
		Type:      EntryTypeCommand,
		Version:   "1.0.0",
		Timestamp: time.Now().UTC(),
		Session:   session,
		Command:   cmd,
	}
}

// NewExecEntry creates an exec log entry.
func NewExecEntry(session SessionInfo, cmd *CommandInfo, exec *ExecInfo) *LogEntry {
	return &LogEntry{
		Type:      EntryTypeExec,
		Version:   "1.0.0",
		Timestamp: time.Now().UTC(),
		Session:   session,
		Command:   cmd,
		Exec:      exec,
	}
}
