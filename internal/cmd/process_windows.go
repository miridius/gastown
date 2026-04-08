//go:build windows

package cmd

import (
	"os"
	"syscall"
)

// isProcessRunning checks if a process with the given PID exists.
func isProcessRunning(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, err := syscall.OpenProcess(syscall.PROCESS_QUERY_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	syscall.CloseHandle(h)
	return true
}

// killProcess is a helper used on Windows in place of syscall.Kill.
func killProcess(pid int) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return p.Kill()
}
