package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"runx/internal/process"
	"runx/internal/session"
	"runx/internal/socket"
)

type Daemon struct {
	pm *process.Manager
	sm *session.Manager
}

func New() *Daemon {
	return &Daemon{
		pm: process.NewManager(),
		sm: session.NewManager(),
	}
}

func (d *Daemon) ProcessManager() *process.Manager {
	return d.pm
}

func (d *Daemon) SessionManager() *session.Manager {
	return d.sm
}

func (d *Daemon) Run() error {
	removeSocket()

	srv := socket.NewServer(d.pm, d.sm)

	pid := os.Getpid()
	writePidFile(pid)

	go handleSignals(d.pm.Shutdown)

	return srv.Start()
}

func removeSocket() {
	home, _ := os.UserHomeDir()
	os.Remove(home + "/.runx/runx.sock")
}

func removePidFile() {
	home, _ := os.UserHomeDir()
	os.Remove(home + "/.runx/daemon.pid")
}

func writePidFile(pid int) {
	home, _ := os.UserHomeDir()
	content := []byte(fmt.Sprintf("%d", pid))
	os.WriteFile(home+"/.runx/daemon.pid", content, 0644)
}

func readPidFile() (int, error) {
	home, _ := os.UserHomeDir()
	content, err := os.ReadFile(home + "/.runx/daemon.pid")
	if err != nil {
		return 0, err
	}
	var pid int
	fmt.Sscanf(string(content), "%d", &pid)
	return pid, nil
}

func IsRunning() bool {
	_, err := readPidFile()
	if err != nil {
		return false
	}

	client := socket.NewClient()
	if err := client.Ping(); err != nil {
		return false
	}

	return true
}

// Spawn starts the background daemon from the runx binary. The daemon is
// launched as `<executable> daemon`, so the current executable must
// understand that command.
//
// A GUI binary (runx-gui) does NOT: it would spawn endless copies of itself,
// each of which tries to spawn another one. When called from such a binary,
// Spawn falls back to a real `runx` from PATH instead.
func Spawn() error {
	if IsRunning() {
		return nil
	}

	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("executable: %w", err)
	}
	if isGUIExecutable(execPath) {
		if p, err := exec.LookPath("runx"); err == nil {
			execPath = p
		} else {
			return fmt.Errorf("daemon: cannot start from GUI binary and `runx` is not in PATH (start the daemon manually: runx daemon)")
		}
	}

	home, _ := os.UserHomeDir()
	os.MkdirAll(home+"/.runx", 0755)

	nullFile, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("null: %w", err)
	}

	pid, err := os.StartProcess(execPath, []string{execPath, "daemon"}, &os.ProcAttr{
		Files: []*os.File{nullFile, nullFile, nullFile},
		Env:   os.Environ(),
	})
	if err != nil {
		return fmt.Errorf("spawn: %w", err)
	}
	_ = pid

	return waitForSocket(10 * time.Second)
}

// isGUIExecutable reports whether the given binary is the Wails desktop app,
// which must never be used to start the daemon (it would recurse).
func isGUIExecutable(path string) bool {
	base := strings.TrimSuffix(filepath.Base(path), ".exe")
	return strings.HasSuffix(base, "-gui") || strings.HasSuffix(base, "gui")
}

func waitForSocket(timeout time.Duration) error {
	client := socket.NewClient()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if err := client.Ping(); err == nil {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("daemon did not start within %s", timeout)
}

func Kill() error {
	pid, err := readPidFile()
	if err != nil {
		return fmt.Errorf("no daemon pid found")
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		return nil
	}

	return proc.Kill()
}
