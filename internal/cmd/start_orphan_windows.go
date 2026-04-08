//go:build windows

package cmd

// cleanupOrphanedClaude is a no-op on Windows (process management requires Unix signals).
func cleanupOrphanedClaude(_ int) {}

// verifyNoOrphans is a no-op on Windows.
func verifyNoOrphans() {}
