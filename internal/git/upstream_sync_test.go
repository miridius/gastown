package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// setupUpstreamTestRepos creates a simulated fork setup:
//   - upstream: bare repo with commits
//   - origin: clone of upstream (the "fork")
//   - work: clone of origin with upstream remote added
func setupUpstreamTestRepos(t *testing.T) (workDir string, cleanup func()) {
	t.Helper()
	tmpDir := t.TempDir()

	upstream := filepath.Join(tmpDir, "upstream.git")
	origin := filepath.Join(tmpDir, "origin.git")
	work := filepath.Join(tmpDir, "work")

	// Create upstream bare repo with initial commit
	upstreamWork := filepath.Join(tmpDir, "upstream-work")
	run(t, "", "git", "init", "-b", "main", upstreamWork)
	run(t, upstreamWork, "git", "config", "user.email", "test@test.com")
	run(t, upstreamWork, "git", "config", "user.name", "Test")
	writeFile(t, filepath.Join(upstreamWork, "README.md"), "# Upstream\n")
	run(t, upstreamWork, "git", "add", ".")
	run(t, upstreamWork, "git", "commit", "-m", "initial commit")

	// Create bare clone as "upstream"
	run(t, "", "git", "clone", "--bare", upstreamWork, upstream)

	// Create bare clone as "origin" (the fork)
	run(t, "", "git", "clone", "--bare", upstream, origin)

	// Create working copy from origin
	run(t, "", "git", "clone", "-b", "main", origin, work)
	run(t, work, "git", "config", "user.email", "test@test.com")
	run(t, work, "git", "config", "user.name", "Test")

	// Add upstream remote
	run(t, work, "git", "remote", "add", "upstream", upstream)
	run(t, work, "git", "fetch", "upstream")

	return work, func() {}
}

// addUpstreamCommit adds a new commit to the upstream repo (via a temp worktree).
func addUpstreamCommit(t *testing.T, workDir, filename, content, message string) {
	t.Helper()
	g := NewGit(workDir)
	upstreamURL, err := g.GetUpstreamURL()
	if err != nil {
		t.Fatal(err)
	}

	// Clone upstream, make change, push
	tmpDir := t.TempDir()
	upstreamWork := filepath.Join(tmpDir, "upstream-commit")
	run(t, "", "git", "clone", "-b", "main", upstreamURL, upstreamWork)
	run(t, upstreamWork, "git", "config", "user.email", "test@test.com")
	run(t, upstreamWork, "git", "config", "user.name", "Test")
	writeFile(t, filepath.Join(upstreamWork, filename), content)
	run(t, upstreamWork, "git", "add", ".")
	run(t, upstreamWork, "git", "commit", "-m", message)
	run(t, upstreamWork, "git", "push", "origin", "main")
}

// addLocalCommit adds a commit to origin (the fork) via the working copy.
func addLocalCommit(t *testing.T, workDir, filename, content, message string) {
	t.Helper()
	writeFile(t, filepath.Join(workDir, filename), content)
	run(t, workDir, "git", "add", ".")
	run(t, workDir, "git", "commit", "-m", message)
	run(t, workDir, "git", "push", "origin", "main")
}

func run(t *testing.T, dir string, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("command %s %s failed: %v\noutput: %s", name, strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestUpstreamNewCommits_NoNewCommits(t *testing.T) {
	workDir, cleanup := setupUpstreamTestRepos(t)
	defer cleanup()

	g := NewGit(workDir)
	count, err := g.UpstreamNewCommits("main")
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("expected 0 new commits, got %d", count)
	}
}

func TestUpstreamNewCommits_WithNewCommits(t *testing.T) {
	workDir, cleanup := setupUpstreamTestRepos(t)
	defer cleanup()

	// Add a commit to upstream
	addUpstreamCommit(t, workDir, "new-file.txt", "content", "upstream change")

	// Fetch upstream
	g := NewGit(workDir)
	if err := g.FetchUpstream(); err != nil {
		t.Fatal(err)
	}

	count, err := g.UpstreamNewCommits("main")
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("expected 1 new commit, got %d", count)
	}
}

func TestLocalOnlyCommits_NoLocalCommits(t *testing.T) {
	workDir, cleanup := setupUpstreamTestRepos(t)
	defer cleanup()

	g := NewGit(workDir)
	commits, err := g.LocalOnlyCommits("main")
	if err != nil {
		t.Fatal(err)
	}
	if len(commits) != 0 {
		t.Errorf("expected 0 local-only commits, got %d", len(commits))
	}
}

func TestLocalOnlyCommits_WithLocalCommits(t *testing.T) {
	workDir, cleanup := setupUpstreamTestRepos(t)
	defer cleanup()

	addLocalCommit(t, workDir, "local-change.txt", "local content", "local customisation")

	// Fetch both remotes to update refs
	g := NewGit(workDir)
	if err := g.Fetch("origin"); err != nil {
		t.Fatal(err)
	}

	commits, err := g.LocalOnlyCommits("main")
	if err != nil {
		t.Fatal(err)
	}
	if len(commits) != 1 {
		t.Errorf("expected 1 local-only commit, got %d", len(commits))
	}
}

func TestCheckOverlaps_NoOverlap(t *testing.T) {
	workDir, cleanup := setupUpstreamTestRepos(t)
	defer cleanup()

	// Local change touches file A, upstream change touches file B
	addLocalCommit(t, workDir, "local-only.txt", "local", "local change")
	addUpstreamCommit(t, workDir, "upstream-only.txt", "upstream", "upstream change")

	g := NewGit(workDir)
	_ = g.FetchUpstream()
	_ = g.Fetch("origin")

	overlaps, err := g.CheckOverlaps("main")
	if err != nil {
		t.Fatal(err)
	}
	if len(overlaps) != 0 {
		t.Errorf("expected 0 overlaps, got %d", len(overlaps))
	}
}

func TestCheckOverlaps_WithOverlap(t *testing.T) {
	workDir, cleanup := setupUpstreamTestRepos(t)
	defer cleanup()

	// Both touch README.md
	addLocalCommit(t, workDir, "README.md", "# Local Fork\n", "local readme change")
	addUpstreamCommit(t, workDir, "README.md", "# Updated Upstream\n", "upstream readme change")

	g := NewGit(workDir)
	_ = g.FetchUpstream()
	_ = g.Fetch("origin")

	overlaps, err := g.CheckOverlaps("main")
	if err != nil {
		t.Fatal(err)
	}
	if len(overlaps) != 1 {
		t.Errorf("expected 1 overlap, got %d", len(overlaps))
	}
	if len(overlaps) > 0 {
		found := false
		for _, f := range overlaps[0].OverlappingFiles {
			if f == "README.md" {
				found = true
			}
		}
		if !found {
			t.Errorf("expected README.md in overlapping files, got %v", overlaps[0].OverlappingFiles)
		}
	}
}

func TestFilesChangedInCommit(t *testing.T) {
	workDir, cleanup := setupUpstreamTestRepos(t)
	defer cleanup()

	addLocalCommit(t, workDir, "test-file.txt", "content", "add test file")

	g := NewGit(workDir)
	// Get the HEAD commit
	head, err := g.Rev("HEAD")
	if err != nil {
		t.Fatal(err)
	}

	files, err := g.FilesChangedInCommit(head)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0] != "test-file.txt" {
		t.Errorf("expected [test-file.txt], got %v", files)
	}
}

func TestSyncBranchName(t *testing.T) {
	name := SyncBranchName()
	if !strings.HasPrefix(name, "upstream-sync/") {
		t.Errorf("expected upstream-sync/ prefix, got %s", name)
	}
}

func TestUpstreamNewCommits_NoUpstreamRemote(t *testing.T) {
	tmpDir := t.TempDir()
	run(t, "", "git", "init", tmpDir)
	run(t, tmpDir, "git", "config", "user.email", "test@test.com")
	run(t, tmpDir, "git", "config", "user.name", "Test")

	g := NewGit(tmpDir)
	_, err := g.UpstreamNewCommits("main")
	if err == nil {
		t.Error("expected error when no upstream remote configured")
	}
}
