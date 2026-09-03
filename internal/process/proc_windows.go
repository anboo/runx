//go:build windows

package process

import (
	"os/exec"
	"syscall"
)

func setProcessGroup(cmd *exec.Cmd) {}

// signalTree falls back to signaling just the immediate process and,
// if it survived, force-killing it.
func signalTree(p *ManagedProcess, sig syscall.Signal) {
	p.mu.RLock()
	cmd := p.cmd
	p.mu.RUnlock()

	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Signal(sig)
	_ = cmd.Process.Kill()
}
