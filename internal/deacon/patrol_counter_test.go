package deacon

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPatrolCounter(t *testing.T) {
	townRoot := t.TempDir()
	deaconDir := filepath.Join(townRoot, "deacon")
	if err := os.MkdirAll(deaconDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Initially should be 0
	count := ReadPatrolCounter(townRoot)
	if count != 0 {
		t.Errorf("expected initial count 0, got %d", count)
	}

	// Increment several times
	for i := 1; i <= 5; i++ {
		got, err := IncrementPatrolCounter(townRoot)
		if err != nil {
			t.Fatalf("increment %d: %v", i, err)
		}
		if got != i {
			t.Errorf("increment %d: expected %d, got %d", i, i, got)
		}
	}

	// Read should return 5
	count = ReadPatrolCounter(townRoot)
	if count != 5 {
		t.Errorf("expected count 5, got %d", count)
	}

	// Reset
	if err := ResetPatrolCounter(townRoot); err != nil {
		t.Fatal(err)
	}

	// Should be back to 0
	count = ReadPatrolCounter(townRoot)
	if count != 0 {
		t.Errorf("expected count 0 after reset, got %d", count)
	}

	// Reset on non-existent file should not error
	if err := ResetPatrolCounter(townRoot); err != nil {
		t.Errorf("reset on missing file should not error: %v", err)
	}
}

func TestPatrolCounterFile(t *testing.T) {
	got := PatrolCounterFile("/gt")
	expected := filepath.Join("/gt", "deacon", "session_patrol_count")
	if got != expected {
		t.Errorf("expected %s, got %s", expected, got)
	}
}

func TestPatrolCounterAge(t *testing.T) {
	townRoot := t.TempDir()

	// Non-existent file should return 0
	age := PatrolCounterAge(townRoot)
	if age != 0 {
		t.Errorf("expected age 0 for missing file, got %v", age)
	}

	// After increment, age should be very small
	if _, err := IncrementPatrolCounter(townRoot); err != nil {
		t.Fatal(err)
	}
	age = PatrolCounterAge(townRoot)
	if age == 0 {
		t.Error("expected non-zero age after increment")
	}
}
