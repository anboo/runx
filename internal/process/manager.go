package process

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/term"
)

type Manager struct {
	mu         sync.RWMutex
	processes  map[string]*ManagedProcess
	events     []Event
	eventMu    sync.RWMutex
	onChange   func()
	onChangeMu sync.RWMutex
}

type ManagedProcess struct {
	ProcessInfo
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	stdout    io.ReadCloser
	stderr    io.ReadCloser
	stdoutBuf *RingBuffer
	stderrBuf *RingBuffer
	cancel    context.CancelFunc
	done      chan struct{}
	// lastOutput is the wall clock of the most recent log line; drives the
	// idle_timeout monitor.
	lastOutput time.Time
	mu         sync.RWMutex
}

// touch records that the process produced output.
func (p *ManagedProcess) touch() {
	p.mu.Lock()
	p.lastOutput = time.Now()
	p.mu.Unlock()
}

func (m *Manager) SetOnChange(fn func()) {
	m.onChangeMu.Lock()
	defer m.onChangeMu.Unlock()
	m.onChange = fn
}

func (m *Manager) notifyChange() {
	m.onChangeMu.RLock()
	fn := m.onChange
	m.onChangeMu.RUnlock()
	if fn != nil {
		fn()
	}
}

func NewManager() *Manager {
	return &Manager{
		processes: make(map[string]*ManagedProcess),
	}
}

func (m *Manager) Start(name, cwd, command string, args []string, env []string, ttl time.Duration, ephemeral bool, idleTimeout time.Duration) (*ManagedProcess, error) {
	// Replacing an existing process with the same name: stop it and remove it
	// so a name never maps to more than one live process.
	m.replaceByName(name)

	id := fmt.Sprintf("%s-%s", name, generateID()[:6])

	absCwd, err := filepath.Abs(cwd)
	if err != nil {
		return nil, fmt.Errorf("cwd: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	p := &ManagedProcess{
		ProcessInfo: ProcessInfo{
			ID:          id,
			Name:        name,
			Command:     command,
			Args:        args,
			CWD:         absCwd,
			Env:         env,
			Status:      StatusStarting,
			Color:       NextColor(),
			TTL:         ttl,
			Ephemeral:   ephemeral,
			IdleTimeout: idleTimeout,
			StartedAt:   nil,
		},
		stdoutBuf: NewRingBuffer(10000),
		stderrBuf: NewRingBuffer(10000),
		cancel:    cancel,
		done:      make(chan struct{}),
		lastOutput: time.Now(),
	}

	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Dir = absCwd
	cmd.Env = append(os.Environ(), env...)
	setProcessGroup(cmd)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("stderr pipe: %w", err)
	}

	p.cmd = cmd
	p.stdin = stdin
	p.stdout = stdout
	p.stderr = stderr

	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("start: %w", err)
	}

	now := time.Now()
	p.PID = cmd.Process.Pid
	p.Status = StatusRunning
	p.StartedAt = &now

	m.mu.Lock()
	m.processes[id] = p
	m.mu.Unlock()

	m.addEvent(Event{
		Type:      EventStarted,
		ProcessID: id,
		Message:   fmt.Sprintf("started %s (pid %d) in %s", command, p.PID, absCwd),
		Timestamp: now.UnixMilli(),
	})

	go m.readOutput(p, stdout, "stdout")
	go m.readOutput(p, stderr, "stderr")

	if ttl > 0 {
		go func() {
			select {
			case <-time.After(ttl):
				m.Stop(id)
			case <-ctx.Done():
			}
		}()
	}

	if idleTimeout > 0 {
		go m.monitorIdle(p, idleTimeout)
	}

	go func() {
		err := cmd.Wait()
		finish := time.Now()
		p.mu.Lock()
		p.FinishedAt = &finish
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				code := exitErr.ExitCode()
				p.ExitCode = &code
				// A signal death (SIGTERM/SIGKILL/...) carries no exit code;
				// record which signal killed it.
				if ws, ok := exitErr.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
					p.ExitSignal = ws.Signal().String()
				}
				if p.ExitSignal != "" {
					p.LastError = fmt.Sprintf("terminated by signal %s", p.ExitSignal)
				} else {
					p.LastError = fmt.Sprintf("exited with code %d", code)
				}
			} else {
				code := 1
				p.ExitCode = &code
				p.LastError = err.Error()
			}
		} else {
			code := 0
			p.ExitCode = &code
		}
		p.Status = StatusExited
		p.mu.Unlock()
		close(p.done)

		msg := fmt.Sprintf("exited with code %d", *p.ExitCode)
		if p.ExitSignal != "" {
			msg = fmt.Sprintf("killed by signal %s", p.ExitSignal)
		}
		m.addEvent(Event{
			Type:      EventExited,
			ProcessID: id,
			Message:   msg,
			Timestamp: finish.UnixMilli(),
		})
	}()

	m.notifyChange()
	// Metrics collection always runs, regardless of how the process was
	// started (socket /start, restart, stack launch). Kept here so a
	// restarted process does not silently lose its metrics goroutine.
	go CollectMetrics(p)
	return p, nil
}

func (m *Manager) readOutput(p *ManagedProcess, r io.Reader, stream string) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		entry := LogEntry{
			Stream:    stream,
			Line:      line,
			Timestamp: time.Now().UnixMilli(),
		}
		if stream == "stdout" {
			p.stdoutBuf.Append(entry)
		} else {
			p.stderrBuf.Append(entry)
		}
		p.touch()
	}
}

// monitorIdle stops the process when it produces no output for the idle
// timeout. The clock starts at spawn and is reset by every log line.
func (m *Manager) monitorIdle(p *ManagedProcess, idle time.Duration) {
	interval := idle / 2
	if interval < 500*time.Millisecond {
		interval = 500 * time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for range ticker.C {
		p.mu.RLock()
		status := p.Status
		last := p.lastOutput
		p.mu.RUnlock()

		if status != StatusRunning && status != StatusStarting {
			return
		}
		if time.Since(last) > idle {
			p.mu.RLock()
			pid := p.ID
			p.mu.RUnlock()
			m.addEvent(Event{
				Type:      EventStopped,
				ProcessID: pid,
				Message:   fmt.Sprintf("stopped: idle for %s", idle),
				Timestamp: time.Now().UnixMilli(),
			})
			_ = m.Stop(pid)
			return
		}
	}
}

func (m *Manager) Stop(id string) error {
	p := m.findProcess(id)
	if p == nil {
		return fmt.Errorf("process %s not found", id)
	}

	p.mu.Lock()
	if p.Status != StatusRunning && p.Status != StatusStarting {
		p.mu.Unlock()
		return fmt.Errorf("process %s is not running", id)
	}
	p.mu.Unlock()

	// Ask the whole process tree to shut down, then force-kill leftovers.
	signalTree(p, syscall.SIGTERM)
	select {
	case <-p.done:
	case <-time.After(3 * time.Second):
		signalTree(p, syscall.SIGKILL)
		p.cancel()
		select {
		case <-p.done:
		case <-time.After(2 * time.Second):
		}
	}

	m.addEvent(Event{
		Type:      EventStopped,
		ProcessID: p.ID,
		Message:   "stopped via SIGTERM",
		Timestamp: time.Now().UnixMilli(),
	})

	m.notifyChange()
	return nil
}

func (m *Manager) Kill(id string) error {
	p := m.findProcess(id)
	if p == nil {
		return fmt.Errorf("process %s not found", id)
	}

	signalTree(p, syscall.SIGKILL)
	p.cancel()

	now := time.Now()
	p.mu.Lock()
	p.Status = StatusKilled
	p.FinishedAt = &now
	code := -1
	p.ExitCode = &code
	p.ExitSignal = "SIGKILL"
	p.LastError = "killed via SIGKILL"
	p.mu.Unlock()

	m.addEvent(Event{
		Type:      EventKilled,
		ProcessID: p.ID,
		Message:   "killed via SIGKILL",
		Timestamp: now.UnixMilli(),
	})

	m.notifyChange()
	return nil
}

func (m *Manager) Restart(id string) error {
	p := m.findProcess(id)
	if p == nil {
		return fmt.Errorf("process %s not found", id)
	}

	p.mu.Lock()
	name, cwd, cmd := p.Name, p.CWD, p.Command
	args := make([]string, len(p.Args))
	copy(args, p.Args)
	env := make([]string, len(p.Env))
	copy(env, p.Env)
	ttl := p.TTL
	ephemeral := p.Ephemeral
	idle := p.IdleTimeout
	restartCount := p.RestartCount + 1
	oldID := p.ID
	p.mu.Unlock()

	m.Stop(oldID)

	// Start already replaces any process with the same name, so the old
	// instance is dropped from the map here and the new one takes its place.
	newP, err := m.Start(name, cwd, cmd, args, env, ttl, ephemeral, idle)
	if err != nil {
		return fmt.Errorf("restart: %w", err)
	}

	newP.RestartCount = restartCount

	m.addEvent(Event{
		Type:      EventRestarted,
		ProcessID: newP.ID,
		Message:   fmt.Sprintf("restarted %s -> %s (attempt %d)", oldID, newP.ID, restartCount),
		Timestamp: time.Now().UnixMilli(),
	})

	m.notifyChange()
	return nil
}

func (m *Manager) Exec(id string, args []string) (string, error) {
	p := m.findProcess(id)
	if p == nil {
		return "", fmt.Errorf("process %s not found", id)
	}

	p.mu.RLock()
	cwd := p.CWD
	env := append(os.Environ(), p.Env...)
	p.mu.RUnlock()

	if len(args) == 0 {
		return "", fmt.Errorf("no command specified")
	}

	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = cwd
	cmd.Env = env

	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("exec: %w", err)
	}

	return string(out), nil
}

func (m *Manager) Shell(id string, shellCmd []string) error {
	p := m.findProcess(id)
	if p == nil {
		return fmt.Errorf("process %s not found", id)
	}

	p.mu.RLock()
	cwd := p.CWD
	env := append(os.Environ(), p.Env...)
	p.mu.RUnlock()

	shell := "/bin/bash"
	if len(shellCmd) > 0 {
		shell = shellCmd[0]
	}

	cmd := exec.Command(shell, shellCmd[1:]...)
	cmd.Dir = cwd
	cmd.Env = env
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if term.IsTerminal(int(os.Stdin.Fd())) {
		oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
		if err == nil {
			defer term.Restore(int(os.Stdin.Fd()), oldState)
		}
	}

	return cmd.Run()
}

func (m *Manager) Attach(id string) error {
	p := m.findProcess(id)
	if p == nil {
		return fmt.Errorf("process %s not found", id)
	}

	p.mu.RLock()
	status := p.Status
	p.mu.RUnlock()

	if status != StatusRunning {
		return fmt.Errorf("process %s is not running", id)
	}

	done := make(chan struct{}, 2)

	go func() {
		scanner := bufio.NewScanner(p.stdout)
		for scanner.Scan() {
			os.Stdout.WriteString(scanner.Text() + "\n")
		}
		done <- struct{}{}
	}()

	go func() {
		scanner := bufio.NewScanner(p.stderr)
		for scanner.Scan() {
			os.Stderr.WriteString(scanner.Text() + "\n")
		}
		done <- struct{}{}
	}()

	go func() {
		io.Copy(p.stdin, os.Stdin)
		p.stdin.Close()
		done <- struct{}{}
	}()

	<-done
	return nil
}

func (m *Manager) Wait(id string, timeout time.Duration) error {
	p := m.findProcess(id)
	if p == nil {
		return fmt.Errorf("process %s not found", id)
	}

	t := time.NewTimer(timeout)
	defer t.Stop()

	select {
	case <-p.done:
		p.mu.RLock()
		defer p.mu.RUnlock()
		if p.ExitCode != nil && *p.ExitCode != 0 {
			return fmt.Errorf("process exited with code %d", *p.ExitCode)
		}
		return nil
	case <-t.C:
		return fmt.Errorf("timeout waiting for process %s", id)
	}
}

func (m *Manager) List() []ProcessInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	res := make([]ProcessInfo, 0, len(m.processes))
	for _, p := range m.processes {
		p.mu.RLock()
		info := p.ProcessInfo
		if info.Status == StatusRunning && info.StartedAt != nil {
			info.Uptime = time.Since(*info.StartedAt).Round(time.Second).String()
		}
		p.mu.RUnlock()
		info.Events = m.eventsByProcess(info.ID, 10)
		p.mu.RLock()
		if p.Metrics != nil {
			info.Metrics = p.Metrics
		}
		p.mu.RUnlock()
		res = append(res, info)
	}
	return res
}

func (m *Manager) Get(id string) (*ProcessInfo, error) {
	p := m.findProcess(id)
	if p == nil {
		return nil, fmt.Errorf("process %s not found", id)
	}

	p.mu.RLock()
	info := p.ProcessInfo
	if info.Status == StatusRunning && info.StartedAt != nil {
		info.Uptime = time.Since(*info.StartedAt).Round(time.Second).String()
	}
	p.mu.RUnlock()

	info.Events = m.eventsByProcess(info.ID, 50)
	p.mu.RLock()
	if p.Metrics != nil {
		info.Metrics = p.Metrics
	}
	p.mu.RUnlock()

	return &info, nil
}

func (m *Manager) Logs(id string, n int) ([]LogEntry, error) {
	p := m.findProcess(id)
	if p == nil {
		return nil, fmt.Errorf("process %s not found", id)
	}

	out := p.stdoutBuf.Lines(n / 2)
	err := p.stderrBuf.Lines(n / 2)

	merged := append(out, err...)
	sort.Slice(merged, func(i, j int) bool {
		return merged[i].Timestamp < merged[j].Timestamp
	})

	if len(merged) > n {
		merged = merged[len(merged)-n:]
	}

	return merged, nil
}

func (m *Manager) Metrics(id string) (*MetricsSnapshot, error) {
	p := m.findProcess(id)
	if p == nil {
		return nil, fmt.Errorf("process %s not found", id)
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.Metrics, nil
}

func (m *Manager) Events() []Event {
	m.eventMu.RLock()
	defer m.eventMu.RUnlock()
	res := make([]Event, len(m.events))
	copy(res, m.events)
	return res
}

func (m *Manager) addEvent(e Event) {
	m.eventMu.Lock()
	m.events = append(m.events, e)
	if len(m.events) > 10000 {
		m.events = m.events[len(m.events)-5000:]
	}
	m.eventMu.Unlock()
}

func (m *Manager) eventsByProcess(id string, n int) []Event {
	m.eventMu.RLock()
	defer m.eventMu.RUnlock()
	var res []Event
	for i := len(m.events) - 1; i >= 0 && len(res) < n; i-- {
		if m.events[i].ProcessID == id || strings.HasPrefix(m.events[i].ProcessID, id) {
			res = append(res, m.events[i])
		}
	}
	for i, j := 0, len(res)-1; i < j; i, j = i+1, j-1 {
		res[i], res[j] = res[j], res[i]
	}
	return res
}

// replaceByName stops and removes every process with the given name so that
// start always ends up with a single live instance. It is safe to call with
// no processes under that name.
func (m *Manager) replaceByName(name string) {
	m.mu.RLock()
	var stale []string
	for id, p := range m.processes {
		if p.Name == name {
			stale = append(stale, id)
		}
	}
	m.mu.RUnlock()

	for _, id := range stale {
		_ = m.Stop(id)
		m.Remove(id)
	}
}

func (m *Manager) findProcess(id string) *ManagedProcess {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if p, ok := m.processes[id]; ok {
		return p
	}

	var fallback *ManagedProcess
	for _, p := range m.processes {
		if strings.HasPrefix(p.ID, id) || p.Name == id {
			if p.Status == StatusRunning {
				return p
			}
			if fallback == nil {
				fallback = p
			}
		}
	}

	return fallback
}

func (m *Manager) FindAllByName(name string) []ProcessInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var res []ProcessInfo
	for _, p := range m.processes {
		if p.Name == name {
			p.mu.RLock()
			info := p.ProcessInfo
			p.mu.RUnlock()
			res = append(res, info)
		}
	}
	return res
}

func (m *Manager) GC() []string {
	return m.gc(false)
}

// GCForce removes every finished process record (exited/killed/failed)
// regardless of age or ephemeral flag. Used by the MCP process_gc tool where
// the caller explicitly asks to clean up.
func (m *Manager) GCForce() []string {
	return m.gc(true)
}

func (m *Manager) gc(force bool) []string {
	m.mu.Lock()
	defer m.mu.Unlock()

	var removed []string
	for id, p := range m.processes {
		p.mu.RLock()
		status := p.Status
		ephemeral := p.Ephemeral
		finished := p.FinishedAt
		p.mu.RUnlock()

		if status == StatusExited || status == StatusKilled || status == StatusFailed {
			if force || ephemeral || (finished != nil && time.Since(*finished) > 5*time.Minute) {
				delete(m.processes, id)
				removed = append(removed, id)
			}
		}
	}

	m.eventMu.Lock()
	if len(m.events) > 10000 {
		m.events = m.events[len(m.events)-5000:]
	}
	m.eventMu.Unlock()

	m.notifyChange()
	return removed
}

func (m *Manager) Remove(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.processes, id)
	m.notifyChange()
}

func (m *Manager) Shutdown() {
	m.mu.RLock()
	procs := make([]*ManagedProcess, 0, len(m.processes))
	for _, p := range m.processes {
		procs = append(procs, p)
	}
	m.mu.RUnlock()

	for _, p := range procs {
		p.mu.RLock()
		status := p.Status
		p.mu.RUnlock()
		if status == StatusRunning || status == StatusStarting {
			signalTree(p, syscall.SIGKILL)
		}
	}
}

func (m *Manager) RunningPIDs() []int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var pids []int
	for _, p := range m.processes {
		p.mu.RLock()
		if p.Status == StatusRunning {
			pids = append(pids, p.PID)
		}
		p.mu.RUnlock()
	}
	return pids
}
