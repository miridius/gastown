//go:build !windows

package util

import "syscall"

// signalProcess sends a signal to a process. sig=0 checks existence.
func signalProcess(pid int, sig syscall.Signal) error {
	return syscall.Kill(pid, sig)
}

// sigTERM is the SIGTERM signal.
const sigTERM = syscall.SIGTERM

// sigKILL is the SIGKILL signal.
const sigKILL = syscall.SIGKILL

// isNoSuchProcess returns true if the error indicates the process doesn't exist.
func isNoSuchProcess(err error) bool {
	return err == syscall.ESRCH
}

// isPermissionError returns true if the error is a permission error,
// meaning the process exists but we can't signal it.
func isPermissionError(err error) bool {
	return err == syscall.EPERM
}
