//go:build windows

package lock

import (
	"fmt"
	"os"
	"sync"
)

// windowsFlockMu serializes flockAcquire calls on Windows where flock(2) is unavailable.
var windowsFlockMu sync.Mutex

// flockAcquire provides in-process locking on Windows (flock(2) is unavailable).
func flockAcquire(path string) (func(), error) {
	// Ensure the lock file exists so callers that check for it aren't surprised.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, fmt.Errorf("opening flock file: %w", err)
	}
	f.Close()

	windowsFlockMu.Lock()
	return func() { windowsFlockMu.Unlock() }, nil
}

// FlockAcquire opens a flock file and acquires an exclusive advisory lock.
func FlockAcquire(path string) (func(), error) {
	return flockAcquire(path)
}

// FlockTryAcquire attempts a non-blocking exclusive advisory lock on the given path.
// On Windows, this always succeeds (in-process mutex only).
func FlockTryAcquire(path string) (func(), bool, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, false, fmt.Errorf("opening flock file: %w", err)
	}
	f.Close()

	if !windowsFlockMu.TryLock() {
		return nil, false, nil
	}
	return func() { windowsFlockMu.Unlock() }, true, nil
}
