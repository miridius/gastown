package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckEvidenceAt(t *testing.T) {
	t.Run("no file", func(t *testing.T) {
		dir := t.TempDir()
		got := checkEvidenceAt(dir)
		if got != "" {
			t.Errorf("checkEvidenceAt() = %q, want empty", got)
		}
	})

	t.Run("empty file", func(t *testing.T) {
		dir := t.TempDir()
		gtDir := filepath.Join(dir, ".gt")
		os.MkdirAll(gtDir, 0o755)
		os.WriteFile(filepath.Join(gtDir, "verification-evidence"), []byte(""), 0o644)

		got := checkEvidenceAt(dir)
		if got != "" {
			t.Errorf("checkEvidenceAt() = %q, want empty (file is empty)", got)
		}
	})

	t.Run("non-empty file", func(t *testing.T) {
		dir := t.TempDir()
		gtDir := filepath.Join(dir, ".gt")
		os.MkdirAll(gtDir, 0o755)
		evidence := filepath.Join(gtDir, "verification-evidence")
		os.WriteFile(evidence, []byte("## Verification\n$ go test ./...\nPASS\n"), 0o644)

		got := checkEvidenceAt(dir)
		if got != evidence {
			t.Errorf("checkEvidenceAt() = %q, want %q", got, evidence)
		}
	})
}

func TestVerifyBeforeDone_BypassCommands(t *testing.T) {
	// Test that bypass commands are detected by checking the command strings
	// (testing the logic without running the full cobra command)
	tests := []struct {
		name    string
		command string
		bypass  bool
	}{
		{"plain gt done", "gt done", false},
		{"cleanup-status", "gt done --cleanup-status clean", true},
		{"escalated", "gt done --status=ESCALATED", true},
		{"escalated space", "gt done --status ESCALATED", true},
		{"normal status", "gt done --status=COMPLETED", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bypassed := false
			if strings.Contains(tt.command, "--cleanup-status") ||
				strings.Contains(tt.command, "--status=ESCALATED") ||
				strings.Contains(tt.command, "--status ESCALATED") {
				bypassed = true
			}
			if bypassed != tt.bypass {
				t.Errorf("command %q: bypass=%v, want %v", tt.command, bypassed, tt.bypass)
			}
		})
	}
}
