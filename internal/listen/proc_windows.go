//go:build windows

package listen

import (
	"os"
	"os/exec"
)

func ownGroup(*exec.Cmd) {}

func interrupt(cmd *exec.Cmd) { _ = cmd.Process.Kill() }

func kill(cmd *exec.Cmd) { _ = cmd.Process.Kill() }

// alive reports whether a process with this PID runs; on Windows
// FindProcess fails for processes that are gone.
func alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	_ = p.Release()
	return true
}
