//go:build !windows

package process

import (
	"os/exec"
	"syscall"
)

// setProcessGroup makes the managed process a new process group leader so the
// whole tree (cmd and its children) can be signaled at once with a negative pid.
func setProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// signalTree delivers sig to the process and everything it spawned.
func signalTree(p *ManagedProcess, sig syscall.Signal) {
	p.mu.RLock()
	pid := p.PID
	cmd := p.cmd
	p.mu.RUnlock()

	if cmd == nil || cmd.Process == nil || pid <= 0 {
		return
	}

	// Negative pid targets the whole process group.
	_ = syscall.Kill(-pid, sig)
	// The leader may have changed groups; hit it directly too.
	_ = syscall.Kill(pid, sig)
}
