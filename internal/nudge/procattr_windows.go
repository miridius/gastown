//go:build windows

package nudge

import (
	"os"
	"syscall"
)

// detachedProcAttr returns nil on Windows (Setpgid is unavailable).
func detachedProcAttr() *syscall.SysProcAttr {
	return nil
}

// isProcessAlive checks if a process is running.
func isProcessAlive(proc *os.Process) bool {
	return proc.Signal(syscall.Signal(0)) == nil
}

// terminateProcess sends a kill signal on Windows.
func terminateProcess(proc *os.Process) error {
	return proc.Kill()
}
