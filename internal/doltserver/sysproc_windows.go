//go:build windows

package doltserver

import (
	"os"
	"os/exec"
	"syscall"
)

// setProcessGroup is a no-op on Windows (Setpgid is unavailable).
func setProcessGroup(_ *exec.Cmd) {}

// processIsAlive checks whether a process with the given PID is still running.
func processIsAlive(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return process.Signal(syscall.Signal(0)) == nil
}

// gracefulTerminate kills the process on Windows (no SIGTERM equivalent).
func gracefulTerminate(p *os.Process) error {
	return p.Kill()
}
