package process

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// HealthCheck describes a periodic HTTP readiness probe attached to a
// managed process. The daemon polls the URL and emits health_failed /
// health_recovered events, so a process that is alive but not serving
// (e.g. a port collision) is visible instead of looking healthy.
type HealthCheck struct {
	URL      string        `json:"url"`
	Timeout  time.Duration `json:"timeout,omitempty"`
	Interval time.Duration `json:"interval,omitempty"`
}

// SetHealth attaches a periodic health probe to a process. The probe runs
// until the process exits. State changes update the process Healthy flag,
// LastError and the event timeline.
func (m *Manager) SetHealth(id string, hc *HealthCheck) error {
	p := m.findProcess(id)
	if p == nil {
		return fmt.Errorf("process %s not found", id)
	}
	if hc == nil || hc.URL == "" {
		return fmt.Errorf("health url required")
	}
	interval := hc.Interval
	if interval <= 0 {
		interval = time.Second
	}
	timeout := hc.Timeout
	if timeout <= 0 {
		timeout = 2 * time.Second
	}

	p.mu.Lock()
	p.Health = hc
	p.mu.Unlock()

	go m.monitorHealth(p, interval, timeout)
	return nil
}

func (m *Manager) monitorHealth(p *ManagedProcess, interval, timeout time.Duration) {
	client := &http.Client{Timeout: timeout}
	// lastOK tracks the previous probe result; nil means "not probed yet".
	// Events fire only on state transitions, so a permanently unhealthy
	// process does not spam the timeline.
	var lastOK *bool
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	transition := func(ok bool, reason string) {
		if lastOK != nil && *lastOK == ok {
			return
		}
		lastOK = &ok
		now := time.Now()
		p.mu.Lock()
		p.Healthy = ok
		if ok {
			p.LastError = ""
		} else {
			p.LastError = fmt.Sprintf("health check failed: %s (%s)", p.Health.URL, reason)
		}
		p.mu.Unlock()
		if ok {
			m.addEvent(Event{
				Type:      EventHealthRecovered,
				ProcessID: p.ID,
				Message:   fmt.Sprintf("health check ok: %s", p.Health.URL),
				Timestamp: now.UnixMilli(),
			})
		} else {
			m.addEvent(Event{
				Type:      EventHealthFailed,
				ProcessID: p.ID,
				Message:   fmt.Sprintf("health check failed: %s (%s)", p.Health.URL, reason),
				Timestamp: now.UnixMilli(),
			})
		}
		m.notifyChange()
	}

	for range ticker.C {
		p.mu.RLock()
		status := p.Status
		p.mu.RUnlock()
		if status != StatusRunning {
			return
		}

		// If the probe targets a local port, verify the managed process
		// itself is the listener. A foreign service squatting on the same
		// port (e.g. a stale instance) must not count as healthy.
		if port := portFromURL(p.Health.URL); port > 0 && !p.listensOn(port) {
			transition(false, fmt.Sprintf("process does not listen on port %d (occupied by another process?)", port))
			continue
		}

		resp, err := client.Get(p.Health.URL)
		ok := err == nil && resp.StatusCode < 500
		if resp != nil {
			resp.Body.Close()
		}
		if !ok {
			reason := "unreachable"
			if err != nil {
				reason = err.Error()
			}
			transition(false, reason)
			continue
		}
		transition(true, "")
	}
}

// portFromURL extracts the port from an http(s) URL, or 0 when absent.
func portFromURL(raw string) int {
	u, err := url.Parse(raw)
	if err != nil {
		return 0
	}
	if u.Port() != "" {
		if n, err := strconv.Atoi(u.Port()); err == nil {
			return n
		}
	}
	switch u.Scheme {
	case "http":
		return 80
	case "https":
		return 443
	}
	return 0
}