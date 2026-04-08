//go:build windows

package daemon

import (
	"os"
	"os/exec"
)

// setSysProcAttr is a no-op on Windows.
func setSysProcAttr(_ *exec.Cmd) {}

// isProcessAlive checks if a process is still running.
func isProcessAlive(p *os.Process) bool {
	return p.Signal(os.Kill) == nil
}

// sendTermSignal terminates the process on Windows (no SIGTERM).
func sendTermSignal(p *os.Process) error {
	return p.Kill()
}

// sendKillSignal kills the process on Windows.
func sendKillSignal(p *os.Process) error {
	return p.Kill()
}
