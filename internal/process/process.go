package process

import (
	"crypto/rand"
	"fmt"
	"sync"
	"time"
)

type Status string

const (
	StatusRunning  Status = "running"
	StatusStopped  Status = "stopped"
	StatusExited   Status = "exited"
	StatusKilled   Status = "killed"
	StatusStarting Status = "starting"
	StatusFailed   Status = "failed"
	StatusWaiting  Status = "waiting"
)

type EventType string

const (
	EventStarted         EventType = "started"
	EventReady           EventType = "ready"
	EventStopped         EventType = "stopped"
	EventRestarted       EventType = "restarted"
	EventExited          EventType = "exited"
	EventKilled          EventType = "killed"
	EventHealthFailed    EventType = "health_failed"
	EventHealthRecovered EventType = "health_recovered"
	EventLogOutput       EventType = "log_output"
)

type Event struct {
	Type      EventType `json:"type"`
	ProcessID string    `json:"process_id"`
	Message   string    `json:"message,omitempty"`
	Timestamp int64     `json:"timestamp"`
}

type LogEntry struct {
	Stream    string `json:"stream"`
	Line      string `json:"line"`
	Timestamp int64  `json:"timestamp"`
}

type MetricsSnapshot struct {
	CPU             float64 `json:"cpu"`
	Memory          uint64  `json:"memory"`
	RSS             uint64  `json:"rss"`
	VirtualMemory   uint64  `json:"virtual_memory"`
	Threads         int32   `json:"threads"`
	FDCount         int     `json:"fd_count"`
	NetworkRX       uint64  `json:"network_rx"`
	NetworkTX       uint64  `json:"network_tx"`
	DiskRead        uint64  `json:"disk_read"`
	DiskWrite       uint64  `json:"disk_write"`
	IOWait          float64 `json:"io_wait"`
	ContextSwitches int64   `json:"context_switches"`
	Timestamp       int64   `json:"timestamp"`
}

type ProcessInfo struct {
	ID            string           `json:"id"`
	Name          string           `json:"name"`
	PID           int              `json:"pid"`
	Command       string           `json:"command"`
	Args          []string         `json:"args"`
	CWD           string           `json:"cwd"`
	Env           []string         `json:"env"`
	Status        Status           `json:"status"`
	StartedAt     *time.Time       `json:"started_at,omitempty"`
	FinishedAt    *time.Time       `json:"finished_at,omitempty"`
	ExitCode      *int             `json:"exit_code,omitempty"`
	// ExitSignal is set when the process was terminated by a signal (e.g. SIGKILL).
	ExitSignal string `json:"exit_signal,omitempty"`
	// LastError carries the failure reason (exec error, crash, stop/kill) so
	// UIs and agents can show why a process is not running.
	LastError    string           `json:"last_error,omitempty"`
	Color        string           `json:"color"`
	RestartCount int              `json:"restart_count"`
	TTL          time.Duration    `json:"ttl,omitempty"`
	Ephemeral    bool             `json:"ephemeral"`
	IdleTimeout  time.Duration    `json:"idle_timeout,omitempty"`
	// Health is the attached readiness probe, if any.
	Health *HealthCheck `json:"health,omitempty"`
	// Healthy is the last known probe result (true once the first probe
	// succeeds; false when the probe is failing or not yet probed).
	Healthy bool `json:"healthy"`
	Events  []Event          `json:"events"`
	Metrics *MetricsSnapshot `json:"metrics,omitempty"`
	Uptime  string           `json:"uptime,omitempty"`
}

var ProcessColors = []string{
	"#00BFFF", "#32CD32", "#9370DB", "#FFD700", "#FF6347",
	"#00CED1", "#FF69B4", "#87CEEB", "#98FB98", "#DDA0DD",
	"#F0E68C", "#FFA07A", "#20B2AA", "#B0C4DE", "#FA8072",
	"#7B68EE", "#48D1CC", "#C71585", "#00FA9A", "#FF8C00",
}

var colorIndex int
var colorMu sync.Mutex

func NextColor() string {
	colorMu.Lock()
	defer colorMu.Unlock()
	c := ProcessColors[colorIndex%len(ProcessColors)]
	colorIndex++
	return c
}

func generateID() string {
	b := make([]byte, 6)
	rand.Read(b)
	return fmt.Sprintf("%x", b)
}
