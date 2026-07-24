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
	mu        sync.RWMutex
	processes map[string]*ManagedProcess
	events    []Event
	eventMu   sync.RWMutex
	onChange  func()
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
	mu        sync.RWMutex
}

func (m *Manager) SetOnChange(fn func()) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onChange = fn
}

func (m *Manager) notifyChange() {
	m.mu.RLock()
	fn := m.onChange
	m.mu.RUnlock()
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
	}

	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Dir = absCwd
	cmd.Env = append(os.Environ(), env...)

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

	go func() {
		err := cmd.Wait()
		finish := time.Now()
		p.mu.Lock()
		p.FinishedAt = &finish
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				code := exitErr.ExitCode()
				p.ExitCode = &code
			} else {
				code := 1
				p.ExitCode = &code
			}
		} else {
			code := 0
			p.ExitCode = &code
		}
		p.Status = StatusExited
		p.mu.Unlock()
		close(p.done)

		m.addEvent(Event{
			Type:      EventExited,
			ProcessID: id,
			Message:   fmt.Sprintf("exited with code %d", *p.ExitCode),
			Timestamp: finish.UnixMilli(),
		})
	}()

	m.notifyChange()
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

	p.cmd.Process.Signal(syscall.SIGTERM)
	p.cancel()

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

	p.cmd.Process.Kill()
	p.cancel()

	now := time.Now()
	p.mu.Lock()
	p.Status = StatusKilled
	p.FinishedAt = &now
	code := -1
	p.ExitCode = &code
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
	p.mu.Unlock()

	m.Stop(p.ID)
	select {
	case <-p.done:
	case <-time.After(5 * time.Second):
	}

	newP, err := m.Start(name, cwd, cmd, args, env, ttl, ephemeral, idle)
	if err != nil {
		return fmt.Errorf("restart: %w", err)
	}

	newP.RestartCount = restartCount

	m.mu.Lock()
	oldID := p.ID
	delete(m.processes, oldID)
	m.processes[newP.ID] = newP
	m.mu.Unlock()

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
			if ephemeral || (finished != nil && time.Since(*finished) > 5*time.Minute) {
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
			p.cmd.Process.Kill()
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
