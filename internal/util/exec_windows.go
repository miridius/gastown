//go:build windows

package util

import "os/exec"

// SetProcessGroup is a no-op on Windows (Setpgid and SIGKILL are unavailable).
func SetProcessGroup(cmd *exec.Cmd) {}

// SetDetachedProcessGroup is a no-op on Windows.
func SetDetachedProcessGroup(cmd *exec.Cmd) {}
