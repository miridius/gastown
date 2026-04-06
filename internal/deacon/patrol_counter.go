// Package deacon provides the Deacon agent infrastructure.
// ABOUTME: Session-scoped patrol cycle counter to enforce batch limits.
// ABOUTME: Prevents the deacon from running indefinitely without session handoff.

package deacon

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// DefaultMaxPatrolCycles is the maximum number of patrol cycles per session
// before the deacon must hand off to a fresh session. This prevents context
// degradation and ensures clean session boundaries.
const DefaultMaxPatrolCycles = 20

// PatrolCounterFile returns the path to the session patrol counter file.
func PatrolCounterFile(townRoot string) string {
	return filepath.Join(townRoot, "deacon", "session_patrol_count")
}

// ReadPatrolCounter reads the current patrol cycle count for this session.
// Returns 0 if the file doesn't exist or can't be read.
func ReadPatrolCounter(townRoot string) int {
	data, err := os.ReadFile(PatrolCounterFile(townRoot))
	if err != nil {
		return 0
	}
	count, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0
	}
	return count
}

// IncrementPatrolCounter increments the session patrol counter and returns
// the new count.
func IncrementPatrolCounter(townRoot string) (int, error) {
	count := ReadPatrolCounter(townRoot) + 1
	counterFile := PatrolCounterFile(townRoot)

	if err := os.MkdirAll(filepath.Dir(counterFile), 0755); err != nil {
		return count, err
	}

	data := []byte(strconv.Itoa(count))
	return count, os.WriteFile(counterFile, data, 0600)
}

// ResetPatrolCounter resets the session patrol counter to zero.
// Called when a new deacon session starts.
func ResetPatrolCounter(townRoot string) error {
	counterFile := PatrolCounterFile(townRoot)
	err := os.Remove(counterFile)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// PatrolCounterAge returns how old the counter file is.
// Returns 0 if the file doesn't exist.
func PatrolCounterAge(townRoot string) time.Duration {
	info, err := os.Stat(PatrolCounterFile(townRoot))
	if err != nil {
		return 0
	}
	return time.Since(info.ModTime())
}
