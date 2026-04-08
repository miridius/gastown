package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestDetectCrewWorker_FromEnv(t *testing.T) {
	t.Setenv("GT_CREW", "alan")
	got := detectCrewWorker("/gt", "meerkat")
	if got != "alan" {
		t.Errorf("detectCrewWorker() = %q, want %q", got, "alan")
	}
}

func TestDetectCrewWorker_FromCwd(t *testing.T) {
	t.Setenv("GT_CREW", "") // Ensure env var is unset

	// Create a temp directory structure simulating a crew worktree
	townRoot := t.TempDir()
	crewDir := filepath.Join(townRoot, "meerkat", "crew", "bob", "meerkat")
	if err := os.MkdirAll(crewDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Change to crew dir
	origDir, _ := os.Getwd()
	defer func() { _ = os.Chdir(origDir) }()
	if err := os.Chdir(crewDir); err != nil {
		t.Fatal(err)
	}

	got := detectCrewWorker(townRoot, "meerkat")
	if got != "bob" {
		t.Errorf("detectCrewWorker() = %q, want %q", got, "bob")
	}
}

func TestDetectCrewWorker_NoIdentity(t *testing.T) {
	t.Setenv("GT_CREW", "")

	// Not in a crew directory
	townRoot := t.TempDir()
	origDir, _ := os.Getwd()
	defer func() { _ = os.Chdir(origDir) }()
	if err := os.Chdir(townRoot); err != nil {
		t.Fatal(err)
	}

	got := detectCrewWorker(townRoot, "meerkat")
	if got != "" {
		t.Errorf("detectCrewWorker() = %q, want empty", got)
	}
}

// runGitInDir runs a git command in the specified directory.
func runGitInDir(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %s: %v", args, string(out), err)
	}
}

func TestGenerateBranchSlug_FromCommitMessage(t *testing.T) {
	dir := t.TempDir()
	runGitInDir(t, dir, "init")
	runGitInDir(t, dir, "config", "user.email", "test@test.com")
	runGitInDir(t, dir, "config", "user.name", "Test")

	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}
	runGitInDir(t, dir, "add", "file.txt")
	runGitInDir(t, dir, "commit", "-m", "feat: add new dashboard widget (gt-abc)")

	slug := generateBranchSlug(dir)
	if slug != "add-new-dashboard-widget" {
		t.Errorf("generateBranchSlug() = %q, want %q", slug, "add-new-dashboard-widget")
	}
}

func TestGenerateBranchSlug_NoPrefix(t *testing.T) {
	dir := t.TempDir()
	runGitInDir(t, dir, "init")
	runGitInDir(t, dir, "config", "user.email", "test@test.com")
	runGitInDir(t, dir, "config", "user.name", "Test")

	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}
	runGitInDir(t, dir, "add", "file.txt")
	runGitInDir(t, dir, "commit", "-m", "Update README with setup instructions")

	slug := generateBranchSlug(dir)
	if slug != "update-readme-with-setup-instructions" {
		t.Errorf("generateBranchSlug() = %q, want %q", slug, "update-readme-with-setup-instructions")
	}
}

func TestGenerateBranchSlug_Truncation(t *testing.T) {
	dir := t.TempDir()
	runGitInDir(t, dir, "init")
	runGitInDir(t, dir, "config", "user.email", "test@test.com")
	runGitInDir(t, dir, "config", "user.name", "Test")

	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}
	runGitInDir(t, dir, "add", "file.txt")
	runGitInDir(t, dir, "commit", "-m", "This is a very long commit message that should be truncated to a reasonable branch name length for readability")

	slug := generateBranchSlug(dir)
	if len(slug) > 40 {
		t.Errorf("generateBranchSlug() length = %d, want <= 40", len(slug))
	}
	// Should not end with hyphen
	if slug[len(slug)-1] == '-' {
		t.Errorf("generateBranchSlug() = %q, should not end with hyphen", slug)
	}
}

func TestGenerateBranchSlug_EmptyRepo(t *testing.T) {
	dir := t.TempDir()
	slug := generateBranchSlug(dir)
	if slug != "push" {
		t.Errorf("generateBranchSlug() = %q, want %q", slug, "push")
	}
}
