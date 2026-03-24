package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/steveyegge/gastown/internal/workspace"
)

// HookCacheEntry represents the cached hook state for an agent.
// An empty BeadID means no work is hooked.
type HookCacheEntry struct {
	BeadID   string `json:"bead_id,omitempty"`
	Subject  string `json:"subject,omitempty"`
	HookedAt string `json:"hooked_at,omitempty"`
}

// hookCachePath returns the path to the hook cache file for the given agent.
// Path: <town-root>/mayor/<rig>/<role>/<name>/hook.json
// For town-level agents: <town-root>/mayor/<role>/hook.json
func hookCachePath(townRoot, agentID string) string {
	if townRoot == "" || agentID == "" {
		return ""
	}

	parts := strings.Split(strings.TrimRight(agentID, "/"), "/")

	switch len(parts) {
	case 1:
		// Town-level: "mayor", "deacon"
		return filepath.Join(townRoot, "mayor", parts[0], "hook.json")
	case 2:
		// Rig-level singleton: "gastown/witness", "gastown/refinery"
		return filepath.Join(townRoot, "mayor", parts[0], parts[1], "hook.json")
	case 3:
		// Rig-level agent: "gastown/polecats/nitro", "gastown/crew/max"
		return filepath.Join(townRoot, "mayor", parts[0], parts[1], parts[2], "hook.json")
	default:
		return ""
	}
}

// WriteHookCache writes the hook state to the local cache file.
// Writes atomically (tmp + rename) to avoid partial reads.
func WriteHookCache(townRoot, agentID, beadID, subject string) error {
	cachePath := hookCachePath(townRoot, agentID)
	if cachePath == "" {
		return nil // silently skip if we can't determine cache path
	}

	entry := HookCacheEntry{
		BeadID:   beadID,
		Subject:  subject,
		HookedAt: time.Now().UTC().Format(time.RFC3339),
	}

	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshaling hook cache: %w", err)
	}

	// Ensure directory exists
	dir := filepath.Dir(cachePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating hook cache dir: %w", err)
	}

	// Write atomically: write to .tmp then rename
	tmpPath := cachePath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return fmt.Errorf("writing hook cache tmp: %w", err)
	}
	if err := os.Rename(tmpPath, cachePath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("renaming hook cache: %w", err)
	}

	return nil
}

// ClearHookCache clears the hook cache by writing an empty entry.
func ClearHookCache(townRoot, agentID string) error {
	return WriteHookCache(townRoot, agentID, "", "")
}

// ReadHookCache reads the hook cache for the given agent.
// Returns nil if the cache file is missing, empty, or corrupt.
func ReadHookCache(townRoot, agentID string) *HookCacheEntry {
	cachePath := hookCachePath(townRoot, agentID)
	if cachePath == "" {
		return nil
	}

	data, err := os.ReadFile(cachePath)
	if err != nil {
		return nil // missing or unreadable
	}

	var entry HookCacheEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return nil // corrupt
	}

	if entry.BeadID == "" {
		return nil // empty hook
	}

	return &entry
}

// WriteHookCacheFromContext writes the hook cache using the current context
// to determine the town root. Non-fatal: logs to stderr on failure.
func WriteHookCacheFromContext(agentID, beadID, subject string) {
	townRoot, err := workspace.FindFromCwd()
	if err != nil || townRoot == "" {
		return
	}
	if err := WriteHookCache(townRoot, agentID, beadID, subject); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to write hook cache: %v\n", err)
	}
}

// ClearHookCacheFromContext clears the hook cache using the current context.
// Non-fatal: logs to stderr on failure.
func ClearHookCacheFromContext(agentID string) {
	townRoot, err := workspace.FindFromCwd()
	if err != nil || townRoot == "" {
		return
	}
	if err := ClearHookCache(townRoot, agentID); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to clear hook cache: %v\n", err)
	}
}
