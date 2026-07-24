package daemon

import (
	"fmt"
	"os"
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

func Spawn() error {
	if IsRunning() {
		return nil
	}

	home, _ := os.UserHomeDir()
	os.MkdirAll(home+"/.runx", 0755)

	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("executable: %w", err)
	}

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
