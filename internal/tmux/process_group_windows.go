//go:build windows

package tmux

func killProcessGroup(_ int) {
	// No-op: process group signals are not available on Windows.
}

// getParentPID returns the parent process ID (PPID) for a given PID.
func getParentPID(_ string) string {
	return ""
}

// getProcessGroupID returns the process group ID (PGID) for a given PID.
func getProcessGroupID(_ string) string {
	return ""
}

// getProcessGroupMembers returns all PIDs in a process group.
func getProcessGroupMembers(_ string) []string {
	return nil
}
