package refinery

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/steveyegge/gastown/internal/beads"
	"github.com/steveyegge/gastown/internal/rig"
	"github.com/steveyegge/gastown/internal/session"
	"github.com/steveyegge/gastown/internal/testutil"
)

func setupTestRegistry(t *testing.T) {
	t.Helper()
	// Use a prefix that won't collide with real gastown sessions.
	// The "tr" prefix conflicts with actual rigs running on the host
	// (e.g., tr-refinery, tr-witness), causing tests that assert
	// "no session exists" to fail in gastown workspaces.
	reg := session.NewPrefixRegistry()
	reg.Register("xut", "testrig")
	old := session.DefaultRegistry()
	session.SetDefaultRegistry(reg)
	t.Cleanup(func() { session.SetDefaultRegistry(old) })
}

func setupTestManager(t *testing.T) (*Manager, string) {
	t.Helper()
	setupTestRegistry(t)

	// Create temp directory structure
	tmpDir := t.TempDir()
	rigPath := filepath.Join(tmpDir, "testrig")
	if err := os.MkdirAll(filepath.Join(rigPath, ".runtime"), 0755); err != nil {
		t.Fatalf("mkdir .runtime: %v", err)
	}

	r := &rig.Rig{
		Name: "testrig",
		Path: rigPath,
	}

	return NewManager(r), rigPath
}

func TestManager_SessionName(t *testing.T) {
	mgr, _ := setupTestManager(t)

	want := "xut-refinery"
	got := mgr.SessionName()
	if got != want {
		t.Errorf("SessionName() = %s, want %s", got, want)
	}
}

func TestManager_IsRunning_NoSession(t *testing.T) {
	mgr, _ := setupTestManager(t)

	// Without a tmux session, IsRunning should return false
	// Note: this test doesn't create a tmux session, so it tests the "not running" case
	running, err := mgr.IsRunning()
	if err != nil {
		// If tmux server isn't running, HasSession returns an error
		// This is expected in test environments without tmux
		t.Logf("IsRunning returned error (expected without tmux): %v", err)
		return
	}

	if running {
		t.Error("IsRunning() = true, want false (no session created)")
	}
}

func TestManager_Status_NotRunning(t *testing.T) {
	mgr, _ := setupTestManager(t)

	// Without a tmux session, Status should return ErrNotRunning
	_, err := mgr.Status()
	if err == nil {
		t.Error("Status() expected error when not running")
	}
	// May return ErrNotRunning or a tmux server error
	t.Logf("Status returned error (expected): %v", err)
}

func TestManager_Queue_NoBeads(t *testing.T) {
	mgr, _ := setupTestManager(t)

	// Queue returns error when no beads database exists
	// This is expected - beads requires initialization
	_, err := mgr.Queue()
	if err == nil {
		// If beads is somehow available, queue should be empty
		t.Log("Queue() succeeded unexpectedly (beads may be available)")
		return
	}
	// Error is expected when beads isn't initialized
	t.Logf("Queue() returned error (expected without beads): %v", err)
}

func TestManager_Queue_FiltersClosedMergeRequests(t *testing.T) {
	mgr, rigPath := setupTestManager(t)
	testutil.RequireDoltContainer(t)
	port, _ := strconv.Atoi(testutil.DoltContainerPort())
	b := beads.NewIsolatedWithPort(rigPath, port)
	if err := b.Init("gt"); err != nil {
		t.Skipf("bd init unavailable in test environment: %v", err)
	}

	openIssue, err := b.Create(beads.CreateOptions{
		Title: "Open MR",
		Labels: []string{"gt:merge-request"},
	})
	if err != nil {
		t.Fatalf("create open merge-request issue: %v", err)
	}
	closedIssue, err := b.Create(beads.CreateOptions{
		Title: "Closed MR",
		Labels: []string{"gt:merge-request"},
	})
	if err != nil {
		t.Fatalf("create closed merge-request issue: %v", err)
	}
	closedStatus := "closed"
	if err := b.Update(closedIssue.ID, beads.UpdateOptions{Status: &closedStatus}); err != nil {
		t.Fatalf("close merge-request issue: %v", err)
	}

	queue, err := mgr.Queue()
	if err != nil {
		t.Fatalf("Queue() error: %v", err)
	}

	var sawOpen bool
	for _, item := range queue {
		if item.MR == nil {
			continue
		}
		if item.MR.ID == closedIssue.ID {
			t.Fatalf("queue contains closed merge-request %s", closedIssue.ID)
		}
		if item.MR.ID == openIssue.ID {
			sawOpen = true
		}
	}
	if !sawOpen {
		t.Fatalf("queue missing expected open merge-request %s", openIssue.ID)
	}
}

func TestManager_FindMR_NoBeads(t *testing.T) {
	mgr, _ := setupTestManager(t)

	// FindMR returns error when no beads database exists
	_, err := mgr.FindMR("nonexistent-mr")
	if err == nil {
		t.Error("FindMR() expected error")
	}
	// Any error is acceptable when beads isn't initialized
	t.Logf("FindMR() returned error (expected): %v", err)
}

func TestManager_RegisterMR_Deprecated(t *testing.T) {
	mgr, _ := setupTestManager(t)

	mr := &MergeRequest{
		ID:     "gt-mr-test",
		Branch: "polecat/Test/gt-123",
		Worker: "Test",
		Status: MROpen,
	}

	// RegisterMR should return an error indicating deprecation
	err := mgr.RegisterMR(mr)
	if err == nil {
		t.Error("RegisterMR() expected error (deprecated)")
	}
}

func TestManager_Retry_Deprecated(t *testing.T) {
	mgr, _ := setupTestManager(t)

	// Retry is deprecated and should not error, just print a message
	err := mgr.Retry("any-id", false)
	if err != nil {
		t.Errorf("Retry() unexpected error: %v", err)
	}
}

func TestCompareScoredIssues_UsesDeterministicIDTieBreaker(t *testing.T) {
	t.Helper()

	first := scoredIssue{
		issue: &beads.Issue{
			ID:        "gt-1",
			CreatedAt: time.Now().UTC().Format(time.RFC3339),
		},
		score: 10,
	}
	second := scoredIssue{
		issue: &beads.Issue{
			ID:        "gt-2",
			CreatedAt: time.Now().UTC().Format(time.RFC3339),
		},
		score: 10,
	}

	if !compareScoredIssues(first, second) {
		t.Fatalf("expected gt-1 to sort before gt-2 for equal scores")
	}
	if compareScoredIssues(second, first) {
		t.Fatalf("expected gt-2 to sort after gt-1 for equal scores")
	}
}

func TestManager_PostMerge_ClosesMRAndSourceIssue(t *testing.T) {
	mgr, rigPath := setupTestManager(t)
	testutil.RequireDoltContainer(t)
	port, _ := strconv.Atoi(testutil.DoltContainerPort())
	b := beads.NewIsolatedWithPort(rigPath, port)
	if err := b.Init("gt"); err != nil {
		t.Skipf("bd init unavailable: %v", err)
	}

	// Create a source issue
	srcIssue, err := b.Create(beads.CreateOptions{
		Title: "Implement feature X",
		Labels: []string{"gt:task"},
	})
	if err != nil {
		t.Fatalf("create source issue: %v", err)
	}

	// Create an MR bead with branch and source_issue fields
	mrDesc := "branch: polecat/test/gt-xyz\nsource_issue: " + srcIssue.ID + "\nworker: test\ntarget: main"
	mrIssue, err := b.Create(beads.CreateOptions{
		Title:       "MR for feature X",
		Labels:      []string{"gt:merge-request"},
		Description: mrDesc,
	})
	if err != nil {
		t.Fatalf("create MR issue: %v", err)
	}

	// Run PostMerge
	result, err := mgr.PostMerge(mrIssue.ID)
	if err != nil {
		t.Fatalf("PostMerge() error: %v", err)
	}

	// Verify result
	if !result.MRClosed {
		t.Error("PostMerge() MRClosed = false, want true")
	}
	if !result.SourceIssueClosed {
		t.Error("PostMerge() SourceIssueClosed = false, want true")
	}
	if result.SourceIssueID != srcIssue.ID {
		t.Errorf("PostMerge() SourceIssueID = %s, want %s", result.SourceIssueID, srcIssue.ID)
	}
	if result.MR.Branch != "polecat/test/gt-xyz" {
		t.Errorf("PostMerge() MR.Branch = %s, want polecat/test/gt-xyz", result.MR.Branch)
	}
}

func TestManager_PostMerge_AlreadyClosedMR(t *testing.T) {
	mgr, rigPath := setupTestManager(t)
	testutil.RequireDoltContainer(t)
	port, _ := strconv.Atoi(testutil.DoltContainerPort())
	b := beads.NewIsolatedWithPort(rigPath, port)
	if err := b.Init("gt"); err != nil {
		t.Skipf("bd init unavailable: %v", err)
	}

	// Create and close an MR bead
	mrIssue, err := b.Create(beads.CreateOptions{
		Title:       "Already merged MR",
		Labels:      []string{"gt:merge-request"},
		Description: "branch: polecat/old/gt-old\ntarget: main",
	})
	if err != nil {
		t.Fatalf("create MR issue: %v", err)
	}
	if err := b.Close(mrIssue.ID); err != nil {
		t.Fatalf("close MR issue: %v", err)
	}

	// PostMerge should fail since MR is already closed and won't be in queue
	_, err = mgr.PostMerge(mrIssue.ID)
	if err == nil {
		t.Error("PostMerge() expected error for already-closed MR")
	}
}

func TestManager_PostMerge_NotFound(t *testing.T) {
	mgr, _ := setupTestManager(t)

	_, err := mgr.PostMerge("nonexistent-mr-id")
	if err == nil {
		t.Error("PostMerge() expected error for nonexistent MR")
	}
}

// mockGitClient implements the interface needed by VerifyMergeOnMain,
// including the optional DiffStat and LogGrep methods for fallbacks.
type mockGitClient struct {
	revResults      map[string]string
	revErrors       map[string]error
	ancestorResults map[string]bool
	ancestorErrors  map[string]error
	diffStatResults map[string]string // key: "ref1:ref2"
	diffStatErrors  map[string]error
	logGrepResults  map[string]string // key: "ref:pattern"
	logGrepErrors   map[string]error
}

func (m *mockGitClient) Rev(ref string) (string, error) {
	if err, ok := m.revErrors[ref]; ok {
		return "", err
	}
	if sha, ok := m.revResults[ref]; ok {
		return sha, nil
	}
	return "", fmt.Errorf("unknown ref: %s", ref)
}

func (m *mockGitClient) IsAncestor(ancestor, descendant string) (bool, error) {
	key := ancestor + ":" + descendant
	if err, ok := m.ancestorErrors[key]; ok {
		return false, err
	}
	if result, ok := m.ancestorResults[key]; ok {
		return result, nil
	}
	return false, nil
}

func (m *mockGitClient) DiffStat(ref1, ref2 string) (string, error) {
	key := ref1 + ":" + ref2
	if err, ok := m.diffStatErrors[key]; ok {
		return "", err
	}
	if stat, ok := m.diffStatResults[key]; ok {
		return stat, nil
	}
	return "", fmt.Errorf("no diff stat configured for %s", key)
}

func (m *mockGitClient) LogGrep(ref, pattern string, n int) (string, error) {
	key := ref + ":" + pattern
	if err, ok := m.logGrepErrors[key]; ok {
		return "", err
	}
	if result, ok := m.logGrepResults[key]; ok {
		return result, nil
	}
	return "", nil // no match
}

func TestVerifyMergeOnMain_Success(t *testing.T) {
	client := &mockGitClient{
		revResults: map[string]string{
			"origin/polecat/test/gt-abc": "abc123def456\n",
		},
		ancestorResults: map[string]bool{
			"abc123def456:origin/main": true,
		},
	}

	sha, err := VerifyMergeOnMain(client, "origin/polecat/test/gt-abc", "origin/main")
	if err != nil {
		t.Fatalf("VerifyMergeOnMain() unexpected error: %v", err)
	}
	if sha != "abc123def456" {
		t.Errorf("VerifyMergeOnMain() SHA = %q, want %q", sha, "abc123def456")
	}
}

func TestVerifyMergeOnMain_NotMerged(t *testing.T) {
	client := &mockGitClient{
		revResults: map[string]string{
			"origin/polecat/test/gt-abc": "abc123def456\n",
		},
		ancestorResults: map[string]bool{
			"abc123def456:origin/main": false,
		},
		diffStatResults: map[string]string{
			"origin/polecat/test/gt-abc:origin/main": " file.go | 3 +++\n 1 file changed\n",
		},
	}

	_, err := VerifyMergeOnMain(client, "origin/polecat/test/gt-abc", "origin/main")
	if err == nil {
		t.Fatal("VerifyMergeOnMain() expected error for unmerged branch")
	}
	if !strings.Contains(err.Error(), "NOT reachable") {
		t.Errorf("VerifyMergeOnMain() error = %q, want 'NOT reachable' substring", err.Error())
	}
}

func TestVerifyMergeOnMain_BranchNotFound(t *testing.T) {
	client := &mockGitClient{
		revResults: map[string]string{},
		revErrors: map[string]error{
			"origin/polecat/test/gone": fmt.Errorf("unknown revision"),
		},
	}

	_, err := VerifyMergeOnMain(client, "origin/polecat/test/gone", "origin/main")
	if err == nil {
		t.Fatal("VerifyMergeOnMain() expected error for missing branch")
	}
	if !strings.Contains(err.Error(), "resolving branch") {
		t.Errorf("VerifyMergeOnMain() error = %q, want 'resolving branch' substring", err.Error())
	}
}

func TestVerifyMergeOnMain_SquashMerge_EmptyDiff(t *testing.T) {
	// Squash merge: branch is NOT an ancestor of main, but diff is empty
	// (content was incorporated via squash commit)
	client := &mockGitClient{
		revResults: map[string]string{
			"origin/polecat/test/gt-squash": "48477e8\n",
		},
		ancestorResults: map[string]bool{
			"48477e8:origin/main": false, // squash merge — not an ancestor
		},
		diffStatResults: map[string]string{
			"origin/polecat/test/gt-squash:origin/main": "", // empty diff — content landed
		},
	}

	sha, err := VerifyMergeOnMain(client, "origin/polecat/test/gt-squash", "origin/main")
	if err != nil {
		t.Fatalf("VerifyMergeOnMain() unexpected error for squash merge: %v", err)
	}
	if sha != "48477e8" {
		t.Errorf("VerifyMergeOnMain() SHA = %q, want %q", sha, "48477e8")
	}
}

func TestVerifyMergeOnMain_SquashMerge_NonEmptyDiff(t *testing.T) {
	// Branch is NOT an ancestor and diff is NOT empty — genuinely not merged
	client := &mockGitClient{
		revResults: map[string]string{
			"origin/polecat/test/gt-pending": "e0a0b29\n",
		},
		ancestorResults: map[string]bool{
			"e0a0b29:origin/main": false,
		},
		diffStatResults: map[string]string{
			"origin/polecat/test/gt-pending:origin/main": " internal/foo.go | 5 ++---\n 1 file changed\n",
		},
	}

	_, err := VerifyMergeOnMain(client, "origin/polecat/test/gt-pending", "origin/main")
	if err == nil {
		t.Fatal("VerifyMergeOnMain() expected error for unmerged branch with non-empty diff")
	}
	if !strings.Contains(err.Error(), "NOT reachable") {
		t.Errorf("VerifyMergeOnMain() error = %q, want 'NOT reachable' substring", err.Error())
	}
}

func TestVerifyMergeOnMain_SquashMerge_DiffStatError(t *testing.T) {
	// DiffStat fails — should return an error, not silently pass or fail
	client := &mockGitClient{
		revResults: map[string]string{
			"origin/polecat/test/gt-err": "c397255\n",
		},
		ancestorResults: map[string]bool{
			"c397255:origin/main": false,
		},
		diffStatErrors: map[string]error{
			"origin/polecat/test/gt-err:origin/main": fmt.Errorf("git diff failed"),
		},
	}

	_, err := VerifyMergeOnMain(client, "origin/polecat/test/gt-err", "origin/main")
	if err == nil {
		t.Fatal("VerifyMergeOnMain() expected error when DiffStat fails")
	}
	if !strings.Contains(err.Error(), "squash-merge content") {
		t.Errorf("VerifyMergeOnMain() error = %q, want 'squash-merge content' substring", err.Error())
	}
}

func TestVerifyMergeOnMain_RebaseMerge_IssueHintMatch(t *testing.T) {
	// Rebase merge: branch is NOT an ancestor, diff is NOT empty (base diverged),
	// but a recent commit on main contains the issue ID in its message.
	client := &mockGitClient{
		revResults: map[string]string{
			"origin/polecat/test/gt-rebase": "aaa111\n",
		},
		ancestorResults: map[string]bool{
			"aaa111:origin/main": false,
		},
		diffStatResults: map[string]string{
			"origin/polecat/test/gt-rebase:origin/main": " file.go | 10 ++++\n 1 file changed\n",
		},
		logGrepResults: map[string]string{
			"origin/main:gt-rebase": "bbb222ccc333ddd444eee555fff666\n",
		},
	}

	sha, err := VerifyMergeOnMain(client, "origin/polecat/test/gt-rebase", "origin/main", "gt-rebase")
	if err != nil {
		t.Fatalf("VerifyMergeOnMain() unexpected error for rebase merge: %v", err)
	}
	if sha != "bbb222ccc333ddd444eee555fff666" {
		t.Errorf("VerifyMergeOnMain() SHA = %q, want %q", sha, "bbb222ccc333ddd444eee555fff666")
	}
}

func TestVerifyMergeOnMain_RebaseMerge_NoIssueHint(t *testing.T) {
	// Rebase merge without issue hint: should fail (no way to detect rebase)
	client := &mockGitClient{
		revResults: map[string]string{
			"origin/polecat/test/gt-nohint": "ddd444\n",
		},
		ancestorResults: map[string]bool{
			"ddd444:origin/main": false,
		},
		diffStatResults: map[string]string{
			"origin/polecat/test/gt-nohint:origin/main": " file.go | 5 +++\n 1 file changed\n",
		},
		logGrepResults: map[string]string{
			"origin/main:gt-nohint": "eee555\n",
		},
	}

	_, err := VerifyMergeOnMain(client, "origin/polecat/test/gt-nohint", "origin/main")
	if err == nil {
		t.Fatal("VerifyMergeOnMain() expected error without issue hint")
	}
}

func TestVerifyMergeOnMain_RebaseMerge_NoLogMatch(t *testing.T) {
	// Rebase merge with issue hint but no matching commit on target
	client := &mockGitClient{
		revResults: map[string]string{
			"origin/polecat/test/gt-nomatch": "fff666\n",
		},
		ancestorResults: map[string]bool{
			"fff666:origin/main": false,
		},
		diffStatResults: map[string]string{
			"origin/polecat/test/gt-nomatch:origin/main": " file.go | 3 +++\n 1 file changed\n",
		},
		logGrepResults: map[string]string{
			// No match for this issue ID
		},
	}

	_, err := VerifyMergeOnMain(client, "origin/polecat/test/gt-nomatch", "origin/main", "gt-nomatch")
	if err == nil {
		t.Fatal("VerifyMergeOnMain() expected error when no log match found")
	}
	if !strings.Contains(err.Error(), "NOT reachable") {
		t.Errorf("VerifyMergeOnMain() error = %q, want 'NOT reachable' substring", err.Error())
	}
}
