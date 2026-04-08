//go:build windows

package acp

import (
	"os"
	"time"
)

// signalsToHandle returns the signals that Forward() should listen for.
func signalsToHandle() []os.Signal {
	return []os.Signal{os.Interrupt}
}

// setupProcessGroup is a no-op on Windows.
func (p *Proxy) setupProcessGroup() {}

// isProcessAlive checks if the agent process is still running.
func (p *Proxy) isProcessAlive() bool {
	if p.cmd == nil || p.cmd.Process == nil {
		return false
	}
	// On Windows, Signal(0) is not reliable. Check ProcessState instead.
	return p.cmd.ProcessState == nil
}

// terminateProcess terminates the agent process on Windows.
func (p *Proxy) terminateProcess() {
	if p.cmd != nil && p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
		time.AfterFunc(2*time.Second, func() {
			if p.cmd.ProcessState == nil || !p.cmd.ProcessState.Exited() {
				_ = p.cmd.Process.Kill()
			}
		})
	}
}
