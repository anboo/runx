package process

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	psnet "github.com/shirou/gopsutil/v3/net"
)

// LogQuery filters log reads. Zero values mean "no filter".
type LogQuery struct {
	// Since returns only entries with Timestamp > Since (Unix ms cursor).
	Since int64
	// Stream filters by "stdout" or "stderr". Empty means both.
	Stream string
	// Grep is a regular expression matched against the line text.
	Grep string
	// N caps the number of returned entries, newest kept. 0 means all.
	N int
}

// LogsFiltered reads log lines with cursor and filter support. Unlike Logs,
// it can return lines from the middle of the ring buffer, which is what
// incremental reads (since cursor) need.
func (m *Manager) LogsFiltered(id string, q LogQuery) ([]LogEntry, error) {
	p := m.findProcess(id)
	if p == nil {
		return nil, fmt.Errorf("process %s not found", id)
	}

	var re *regexp.Regexp
	if q.Grep != "" {
		var err error
		re, err = regexp.Compile(q.Grep)
		if err != nil {
			return nil, fmt.Errorf("grep: %w", err)
		}
	}

	out := p.stdoutBuf.All()
	err := p.stderrBuf.All()
	merged := make([]LogEntry, 0, len(out)+len(err))

	for _, e := range append(append([]LogEntry{}, out...), err...) {
		if q.Since > 0 && e.Timestamp <= q.Since {
			continue
		}
		if q.Stream != "" && e.Stream != q.Stream {
			continue
		}
		if re != nil && !re.MatchString(e.Line) {
			continue
		}
		merged = append(merged, e)
	}

	sort.Slice(merged, func(i, j int) bool {
		return merged[i].Timestamp < merged[j].Timestamp
	})

	if q.N > 0 && len(merged) > q.N {
		merged = merged[len(merged)-q.N:]
	}

	return merged, nil
}

// WaitStatus blocks until the process reaches one of the given statuses or
// the timeout expires. It returns the status that matched.
func (m *Manager) WaitStatus(id string, statuses []Status, timeout time.Duration) (Status, error) {
	p := m.findProcess(id)
	if p == nil {
		return "", fmt.Errorf("process %s not found", id)
	}

	deadline := time.Now().Add(timeout)
	accept := make(map[Status]bool, len(statuses))
	for _, s := range statuses {
		accept[s] = true
	}

	for {
		p.mu.RLock()
		status := p.Status
		p.mu.RUnlock()

		if accept[status] {
			return status, nil
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("timeout waiting for %s to reach %v (last status: %s)", id, statuses, status)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// WaitLog blocks until a line matching the pattern appears in the process
// output with Timestamp > since, or the timeout expires. The cursor makes the
// wait atomic: lines already consumed by a previous read are ignored.
func (m *Manager) WaitLog(id, pattern string, since int64, timeout time.Duration) (*LogEntry, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("pattern: %w", err)
	}

	deadline := time.Now().Add(timeout)
	for {
		entries, err := m.LogsFiltered(id, LogQuery{Since: since})
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if re.MatchString(e.Line) {
				// Match in log order: first matching line after the cursor.
				return &e, nil
			}
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("timeout waiting for pattern %q in %s", pattern, id)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// WaitExit blocks until the process exits and returns its exit code.
func (m *Manager) WaitExit(id string, timeout time.Duration) (*int, error) {
	p := m.findProcess(id)
	if p == nil {
		return nil, fmt.Errorf("process %s not found", id)
	}

	t := time.NewTimer(timeout)
	defer t.Stop()

	select {
	case <-p.done:
		p.mu.RLock()
		code := p.ExitCode
		p.mu.RUnlock()
		return code, nil
	case <-t.C:
		return nil, fmt.Errorf("timeout waiting for process %s to exit", id)
	}
}

// listensOn reports whether the managed process tree has a LISTEN socket on
// the given TCP port. The whole tree counts, not just the leader pid, because
// wrappers like `sh -c 'python3 -m http.server'` hand the socket to a child.
// A port occupied by an unrelated process does not count - this is what makes
// wait_port and health probes trustworthy.
func (p *ManagedProcess) listensOn(port int) bool {
	p.mu.RLock()
	pid := p.PID
	p.mu.RUnlock()
	if pid <= 0 {
		return false
	}
	tree, err := buildTree(pid)
	if err != nil || len(tree) == 0 {
		return false
	}
	pidSet := make(map[int]bool, len(tree))
	for _, n := range tree {
		pidSet[n.PID] = true
	}
	conns, err := psnet.Connections("tcp")
	if err != nil {
		return false
	}
	for _, c := range conns {
		if c.Status == "LISTEN" && c.Laddr.Port == uint32(port) && pidSet[int(c.Pid)] {
			return true
		}
	}
	return false
}

// WaitPort blocks until the process is listening on the given TCP port. The
// check is by PID, so a port occupied by an unrelated process does not count
// as ready. It fails fast if the process exits while waiting.
func (m *Manager) WaitPort(id string, port int, timeout time.Duration) error {
	p := m.findProcess(id)
	if p == nil {
		return fmt.Errorf("process %s not found", id)
	}

	deadline := time.Now().Add(timeout)
	for {
		p.mu.RLock()
		status := p.Status
		p.mu.RUnlock()

		if status != StatusRunning {
			return fmt.Errorf("process %s is not running (status: %s)", id, status)
		}

		// Only a LISTEN socket owned by the managed PID counts as ready.
		if p.listensOn(port) {
			return nil
		}

		if time.Now().After(deadline) {
			return fmt.Errorf("timeout waiting for %s to listen on port %d", id, port)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// EventsSince returns events newer than the cursor, optionally filtered by
// process id (exact match or prefix, like findProcess).
func (m *Manager) EventsSince(since int64, processID string) []Event {
	m.eventMu.RLock()
	defer m.eventMu.RUnlock()

	res := make([]Event, 0, len(m.events))
	for _, e := range m.events {
		if since > 0 && e.Timestamp <= since {
			continue
		}
		if processID != "" && e.ProcessID != processID && !strings.HasPrefix(e.ProcessID, processID) {
			continue
		}
		res = append(res, e)
	}
	return res
}
