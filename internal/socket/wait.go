package socket

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"runx/internal/launch"
	"runx/internal/process"
)

// WaitCondition selects what a /wait call blocks on.
type WaitCondition string

const (
	WaitStatus WaitCondition = "status" // process reaches one of Statuses
	WaitExit   WaitCondition = "exit"   // process exits, returns exit code
	WaitLog    WaitCondition = "log"    // a line matching Pattern appears after Since
	WaitPort   WaitCondition = "port"   // process listens on Port (by PID)
)

// WaitRequest is the body of POST /wait/<id>.
type WaitRequest struct {
	Condition WaitCondition `json:"condition"`
	Statuses  []string      `json:"statuses,omitempty"`
	Pattern   string        `json:"pattern,omitempty"`
	Since     int64         `json:"since,omitempty"`
	Port      int           `json:"port,omitempty"`
	Timeout   string        `json:"timeout,omitempty"` // duration string, default 30s, capped 5m
}

// WaitResponse is returned by every wait endpoint. OK=false with Reason
// means the condition was not met within the timeout (or the process died),
// never a protocol error.
type WaitResponse struct {
	Condition WaitCondition `json:"condition"`
	OK        bool          `json:"ok"`
	Reason    string        `json:"reason,omitempty"`
	Status    string        `json:"status,omitempty"`
	ExitCode  *int          `json:"exit_code,omitempty"`
	Line      string        `json:"line,omitempty"`
	Stream    string        `json:"stream,omitempty"`
	Timestamp int64         `json:"timestamp,omitempty"`
}

// WaitHTTPRequest is the body of POST /wait/http.
type WaitHTTPRequest struct {
	URL      string `json:"url"`
	Timeout  string `json:"timeout,omitempty"`  // duration string, default 30s
	Interval string `json:"interval,omitempty"` // duration string, default 1s
}

// HealthFromLaunch converts a launch-config health check (duration strings)
// into a process health probe (durations).
func HealthFromLaunch(h *launch.HealthCheck) *process.HealthCheck {
	hc := &process.HealthCheck{URL: h.URL}
	if h.Timeout != "" {
		if d, err := time.ParseDuration(h.Timeout); err == nil {
			hc.Timeout = d
		}
	}
	if h.Interval != "" {
		if d, err := time.ParseDuration(h.Interval); err == nil {
			hc.Interval = d
		}
	}
	return hc
}

const (
	defaultWaitTimeout = 30 * time.Second
	maxWaitTimeout     = 5 * time.Minute
)

func parseTimeout(s string, def time.Duration) time.Duration {
	if s == "" {
		return def
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return def
	}
	if d > maxWaitTimeout {
		return maxWaitTimeout
	}
	return d
}

func (s *Server) handleWait(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		writeError(w, 405, "method not allowed")
		return
	}

	id := strings.TrimPrefix(r.URL.Path, "/wait/")
	if id == "" {
		writeError(w, 400, "process id required")
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, 400, "bad request")
		return
	}

	var req WaitRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, 400, "invalid json")
		return
	}

	timeout := parseTimeout(req.Timeout, defaultWaitTimeout)
	resp := WaitResponse{Condition: req.Condition}

	switch req.Condition {
	case WaitStatus:
		statuses := make([]process.Status, 0, len(req.Statuses))
		for _, s := range req.Statuses {
			statuses = append(statuses, process.Status(s))
		}
		st, err := s.pm.WaitStatus(id, statuses, timeout)
		if err != nil {
			resp.OK = false
			resp.Reason = err.Error()
		} else {
			resp.OK = true
			resp.Status = string(st)
		}

	case WaitExit:
		code, err := s.pm.WaitExit(id, timeout)
		if err != nil {
			resp.OK = false
			resp.Reason = err.Error()
		} else {
			resp.OK = true
			resp.ExitCode = code
		}

	case WaitLog:
		if req.Pattern == "" {
			writeError(w, 400, "pattern required for log condition")
			return
		}
		entry, err := s.pm.WaitLog(id, req.Pattern, req.Since, timeout)
		if err != nil {
			resp.OK = false
			resp.Reason = err.Error()
		} else {
			resp.OK = true
			resp.Line = entry.Line
			resp.Stream = entry.Stream
			resp.Timestamp = entry.Timestamp
		}

	case WaitPort:
		if req.Port <= 0 {
			writeError(w, 400, "port required for port condition")
			return
		}
		if err := s.pm.WaitPort(id, req.Port, timeout); err != nil {
			resp.OK = false
			resp.Reason = err.Error()
		} else {
			resp.OK = true
		}

	default:
		writeError(w, 400, "unknown condition: "+string(req.Condition))
		return
	}

	writeJSON(w, resp)
}

func (s *Server) handleWaitHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		writeError(w, 405, "method not allowed")
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, 400, "bad request")
		return
	}

	var req WaitHTTPRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, 400, "invalid json")
		return
	}
	if req.URL == "" {
		writeError(w, 400, "url required")
		return
	}

	resp := WaitResponse{Condition: "http"}
	if err := launch.WaitHealthy(&launch.HealthCheck{
		URL:      req.URL,
		Timeout:  req.Timeout,
		Interval: req.Interval,
	}); err != nil {
		resp.OK = false
		resp.Reason = err.Error()
	} else {
		resp.OK = true
	}
	writeJSON(w, resp)
}
