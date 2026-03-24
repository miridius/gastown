package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHookCachePath(t *testing.T) {
	tests := []struct {
		name     string
		townRoot string
		agentID  string
		wantEnd  string // suffix of expected path
	}{
		{
			name:     "polecat",
			townRoot: "/gt",
			agentID:  "gastown/polecats/nitro",
			wantEnd:  "mayor/gastown/polecats/nitro/hook.json",
		},
		{
			name:     "crew",
			townRoot: "/gt",
			agentID:  "gastown/crew/max",
			wantEnd:  "mayor/gastown/crew/max/hook.json",
		},
		{
			name:     "witness",
			townRoot: "/gt",
			agentID:  "gastown/witness",
			wantEnd:  "mayor/gastown/witness/hook.json",
		},
		{
			name:     "mayor",
			townRoot: "/gt",
			agentID:  "mayor/",
			wantEnd:  "mayor/mayor/hook.json",
		},
		{
			name:     "empty agent",
			townRoot: "/gt",
			agentID:  "",
			wantEnd:  "",
		},
		{
			name:     "empty town root",
			townRoot: "",
			agentID:  "gastown/polecats/nitro",
			wantEnd:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hookCachePath(tt.townRoot, tt.agentID)
			if tt.wantEnd == "" {
				if got != "" {
					t.Errorf("hookCachePath(%q, %q) = %q, want empty", tt.townRoot, tt.agentID, got)
				}
				return
			}
			want := filepath.Join(tt.townRoot, tt.wantEnd)
			if got != want {
				t.Errorf("hookCachePath(%q, %q) = %q, want %q", tt.townRoot, tt.agentID, got, want)
			}
		})
	}
}

func TestWriteAndReadHookCache(t *testing.T) {
	tmpDir := t.TempDir()

	agentID := "gastown/polecats/nitro"

	// Initially no cache
	entry := ReadHookCache(tmpDir, agentID)
	if entry != nil {
		t.Fatal("expected nil entry for missing cache")
	}

	// Write cache
	if err := WriteHookCache(tmpDir, agentID, "gt-abc", "Fix the bug"); err != nil {
		t.Fatalf("WriteHookCache: %v", err)
	}

	// Read back
	entry = ReadHookCache(tmpDir, agentID)
	if entry == nil {
		t.Fatal("expected non-nil entry after write")
	}
	if entry.BeadID != "gt-abc" {
		t.Errorf("BeadID = %q, want %q", entry.BeadID, "gt-abc")
	}
	if entry.Subject != "Fix the bug" {
		t.Errorf("Subject = %q, want %q", entry.Subject, "Fix the bug")
	}
	if entry.HookedAt == "" {
		t.Error("HookedAt should not be empty")
	}

	// Clear cache
	if err := ClearHookCache(tmpDir, agentID); err != nil {
		t.Fatalf("ClearHookCache: %v", err)
	}

	// Should return nil after clear (empty bead ID)
	entry = ReadHookCache(tmpDir, agentID)
	if entry != nil {
		t.Error("expected nil entry after clear")
	}
}

func TestReadHookCache_CorruptFile(t *testing.T) {
	tmpDir := t.TempDir()
	agentID := "gastown/polecats/nitro"

	// Write corrupt data
	cachePath := hookCachePath(tmpDir, agentID)
	os.MkdirAll(filepath.Dir(cachePath), 0755)
	os.WriteFile(cachePath, []byte("not json"), 0644)

	entry := ReadHookCache(tmpDir, agentID)
	if entry != nil {
		t.Error("expected nil for corrupt cache file")
	}
}

func TestWriteHookCache_AtomicWrite(t *testing.T) {
	tmpDir := t.TempDir()
	agentID := "gastown/polecats/nitro"

	if err := WriteHookCache(tmpDir, agentID, "gt-xyz", "Test"); err != nil {
		t.Fatalf("WriteHookCache: %v", err)
	}

	// Verify no .tmp file left behind
	cachePath := hookCachePath(tmpDir, agentID)
	tmpPath := cachePath + ".tmp"
	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Error("tmp file should not exist after successful write")
	}

	// Verify the actual file exists
	if _, err := os.Stat(cachePath); err != nil {
		t.Errorf("cache file should exist: %v", err)
	}
}
