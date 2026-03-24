//go:build windows

package tmux

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// acquireFlockLock acquires a file-based lock for cross-process serialization.
// On Windows, flock(2) is not available, so we use exclusive file creation as a
// simple lock mechanism. Returns an unlock function that must be called to release the lock.
func acquireFlockLock(lockPath string, timeout time.Duration) (func(), error) {
	dir := filepath.Dir(lockPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("creating lock dir: %w", err)
	}

	deadline := time.Now().Add(timeout)
	for {
		// O_CREATE|O_EXCL fails if the file already exists, providing mutual exclusion.
		f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0644)
		if err == nil {
			return func() {
				f.Close()
				os.Remove(lockPath)
			}, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("timeout after %s waiting for lock file", timeout)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
