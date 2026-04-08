//go:build windows

package util

import (
	"errors"
	"syscall"
)

// signalProcess is a stub on Windows. sig=0 checks existence via OpenProcess.
func signalProcess(pid int, sig syscall.Signal) error {
	if sig == 0 {
		// Check if process exists by trying to open it.
		h, err := syscall.OpenProcess(syscall.PROCESS_QUERY_INFORMATION, false, uint32(pid))
		if err != nil {
			return err
		}
		syscall.CloseHandle(h)
		return nil
	}
	// Windows does not support Unix signals; return ESRCH-equivalent.
	return errors.New("signals not supported on windows")
}

// sigTERM is a placeholder signal value on Windows.
const sigTERM = syscall.Signal(15)

// sigKILL is a placeholder signal value on Windows.
const sigKILL = syscall.Signal(9)

// isNoSuchProcess returns true if the error indicates the process doesn't exist.
func isNoSuchProcess(_ error) bool {
	return false
}

// isPermissionError returns true if the error is a permission error.
func isPermissionError(_ error) bool {
	return false
}
