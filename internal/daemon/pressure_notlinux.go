//go:build !linux

package daemon

// loadAverage1Sysctl returns the 1-minute load average via sysctl.
// Falls back to 0 on platforms where this is unavailable.
func loadAverage1Sysctl() float64 {
	return 0
}

// availableMemoryGB returns available memory in GB.
// Returns 0 on non-Linux platforms (effectively disabling the check).
func availableMemoryGB() float64 {
	return 0
}
