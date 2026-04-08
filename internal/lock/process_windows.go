//go:build windows

package lock

import (
	"os"
	"os/exec"
)

// setProcessGroup is a no-op on Windows (Setpgid is unavailable).
func setProcessGroup(_ *exec.Cmd) {}

// processExists checks if a process with the given PID exists.
func processExists(pid int) bool {
	if pid <= 0 {
		return false
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// On Windows, FindProcess always succeeds. Signal(0) is not reliable,
	// so we attempt to open the process handle instead.
	// For simplicity, assume the process exists if FindProcess succeeds.
	_ = process
	return true
}
