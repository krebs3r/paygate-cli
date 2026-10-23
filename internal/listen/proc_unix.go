//go:build !windows

package listen

import (
	"os/exec"
	"syscall"
)

// ownGroup starts cloudflared in its own process group, so ctrl+c in the
// terminal reaches paygate only and paygate ends the tunnel itself.
func ownGroup(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }

func interrupt(cmd *exec.Cmd) { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM) }

func kill(cmd *exec.Cmd) { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }

// alive reports whether a process with this PID runs.
func alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}
