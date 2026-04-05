package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/steveyegge/gastown/internal/config"
	gitpkg "github.com/steveyegge/gastown/internal/git"
	"github.com/steveyegge/gastown/internal/rig"
	"github.com/steveyegge/gastown/internal/style"
	"github.com/steveyegge/gastown/internal/workspace"
)

var upstreamCmd = &cobra.Command{
	Use:     "upstream",
	GroupID: GroupWorkspace,
	Short:   "Manage upstream sync for forked rigs",
	Long: `Manage upstream synchronization for rigs that are forks of upstream repositories.

Subcommands:
  sync     Fetch upstream changes, merge, test, and push if clean
  status   Show upstream sync status for one or all rigs`,
	RunE: requireSubcommand,
}

var upstreamSyncCmd = &cobra.Command{
	Use:   "sync [rig]",
	Short: "Sync fork with upstream: fetch, merge, test, push",
	Long: `Fetch upstream changes and merge them into the fork's main branch.

Strategy: merge (not rebase) to preserve local commit history.

Workflow:
  1. Fetch upstream/main
  2. If no new commits: skip
  3. Create sync branch: upstream-sync/YYYY-MM-DD
  4. Merge upstream/main into sync branch
  5. Run full test suite
  6. If clean: fast-forward origin/main, push, delete sync branch
  7. If conflicts or test failures: report details for human resolution

If no rig is specified, syncs all rigs that have an upstream remote configured.

Examples:
  gt upstream sync              # Sync all rigs with upstream
  gt upstream sync gastown      # Sync only gastown
  gt upstream sync --dry-run    # Show what would happen without making changes
  gt upstream sync --skip-tests # Skip test suite (useful for known-good upstream)`,
	Args: cobra.MaximumNArgs(1),
	RunE: runUpstreamSync,
}

var upstreamStatusCmd = &cobra.Command{
	Use:   "status [rig]",
	Short: "Show upstream sync status",
	Long: `Show how far behind each rig's fork is from its upstream.

Displays:
  - New upstream commits not yet merged
  - Local-only commits (fork customisations)
  - Files that overlap between local and upstream changes`,
	Args: cobra.MaximumNArgs(1),
	RunE: runUpstreamStatus,
}

var (
	upstreamDryRun    bool
	upstreamSkipTests bool
)

func init() {
	rootCmd.AddCommand(upstreamCmd)
	upstreamCmd.AddCommand(upstreamSyncCmd)
	upstreamCmd.AddCommand(upstreamStatusCmd)

	upstreamSyncCmd.Flags().BoolVar(&upstreamDryRun, "dry-run", false, "Show what would happen without making changes")
	upstreamSyncCmd.Flags().BoolVar(&upstreamSkipTests, "skip-tests", false, "Skip running the test suite after merge")
}

// rigsWithUpstream returns rigs that have an upstream URL configured.
// If rigName is non-empty, returns only that rig (or error if it has no upstream).
func rigsWithUpstream(rigName string) ([]rigSyncTarget, error) {
	townRoot, err := workspace.FindFromCwdOrError()
	if err != nil {
		return nil, err
	}

	rigsConfigPath := filepath.Join(townRoot, "mayor", "rigs.json")
	rigsConfig, err := config.LoadRigsConfig(rigsConfigPath)
	if err != nil {
		rigsConfig = &config.RigsConfig{Rigs: make(map[string]config.RigEntry)}
	}

	g := gitpkg.NewGit(townRoot)
	rigMgr := rig.NewManager(townRoot, rigsConfig, g)
	rigs, err := rigMgr.DiscoverRigs()
	if err != nil {
		return nil, fmt.Errorf("discovering rigs: %w", err)
	}

	var targets []rigSyncTarget
	for _, r := range rigs {
		cfg, err := rig.LoadRigConfig(r.Path)
		if err != nil {
			continue
		}
		if cfg.UpstreamURL == "" {
			if rigName != "" && r.Name == rigName {
				return nil, fmt.Errorf("rig %q has no upstream URL configured", rigName)
			}
			continue
		}
		if rigName != "" && r.Name != rigName {
			continue
		}

		defaultBranch := cfg.DefaultBranch
		if defaultBranch == "" {
			defaultBranch = "main"
		}

		targets = append(targets, rigSyncTarget{
			rig:           r,
			config:        cfg,
			townRoot:      townRoot,
			defaultBranch: defaultBranch,
		})
	}

	if rigName != "" && len(targets) == 0 {
		return nil, fmt.Errorf("rig %q not found", rigName)
	}
	if len(targets) == 0 {
		return nil, fmt.Errorf("no rigs with upstream remotes configured")
	}

	return targets, nil
}

// rigSyncTarget holds the info needed to sync a single rig.
type rigSyncTarget struct {
	rig           *rig.Rig
	config        *rig.RigConfig
	townRoot      string
	defaultBranch string
}

// bareRepoPath returns the path to the bare repo for this rig.
func (t *rigSyncTarget) bareRepoPath() string {
	return filepath.Join(t.townRoot, t.rig.Name, ".repo.git")
}

func runUpstreamSync(cmd *cobra.Command, args []string) error {
	var rigName string
	if len(args) > 0 {
		rigName = args[0]
	}

	targets, err := rigsWithUpstream(rigName)
	if err != nil {
		return err
	}

	var results []gitpkg.UpstreamSyncResult
	for _, target := range targets {
		result := syncOneRig(target)
		results = append(results, result)
	}

	// Print summary
	fmt.Println()
	fmt.Printf("%s Upstream Sync Summary\n", style.Bold.Render("##"))
	for _, r := range results {
		if r.Error != nil {
			fmt.Printf("  %s %s: %v\n", style.ErrorPrefix, r.RigName, r.Error)
		} else if len(r.ConflictFiles) > 0 {
			fmt.Printf("  %s %s: merge conflicts in %d files (branch: %s)\n",
				style.WarningPrefix, r.RigName, len(r.ConflictFiles), r.SyncBranch)
		} else if r.NewCommits == 0 {
			fmt.Printf("  %s %s: already up to date\n", style.SuccessPrefix, r.RigName)
		} else if r.MergedToMain {
			fmt.Printf("  %s %s: merged %d upstream commits to %s\n",
				style.SuccessPrefix, r.RigName, r.NewCommits, "main")
		} else {
			fmt.Printf("  %s %s: %d new commits on sync branch %s\n",
				style.ArrowPrefix, r.RigName, r.NewCommits, r.SyncBranch)
		}

		// Print overlap warnings
		for _, w := range r.OverlapWarnings {
			fmt.Printf("    %s overlap: %s (%s) — files: %s\n",
				style.WarningPrefix, w.LocalCommit, w.LocalSubject,
				strings.Join(w.OverlappingFiles, ", "))
		}
	}

	// Return error if any sync failed with conflicts or errors
	for _, r := range results {
		if r.Error != nil {
			return fmt.Errorf("one or more syncs failed")
		}
		if len(r.ConflictFiles) > 0 {
			return fmt.Errorf("one or more syncs have unresolved conflicts")
		}
	}

	return nil
}

func syncOneRig(target rigSyncTarget) gitpkg.UpstreamSyncResult {
	result := gitpkg.UpstreamSyncResult{
		RigName:     target.rig.Name,
		UpstreamURL: target.config.UpstreamURL,
	}

	bareRepo := target.bareRepoPath()
	if _, err := os.Stat(bareRepo); os.IsNotExist(err) {
		result.Error = fmt.Errorf("bare repo not found at %s", bareRepo)
		return result
	}

	g := gitpkg.NewGitWithDir(bareRepo, "")

	// Step 1: Ensure upstream remote is configured
	if err := g.AddUpstreamRemote(target.config.UpstreamURL); err != nil {
		result.Error = fmt.Errorf("configuring upstream remote: %w", err)
		return result
	}

	// Step 2: Fetch upstream and origin
	fmt.Printf("%s %s: fetching upstream...\n", style.ArrowPrefix, target.rig.Name)
	if err := g.FetchUpstream(); err != nil {
		result.Error = fmt.Errorf("fetching upstream: %w", err)
		return result
	}
	if err := g.Fetch("origin"); err != nil {
		result.Error = fmt.Errorf("fetching origin: %w", err)
		return result
	}

	// Step 3: Check for new upstream commits
	newCommits, err := g.UpstreamNewCommits(target.defaultBranch)
	if err != nil {
		result.Error = fmt.Errorf("checking upstream commits: %w", err)
		return result
	}
	result.NewCommits = newCommits

	if newCommits == 0 {
		fmt.Printf("  %s already up to date\n", style.SuccessPrefix)
		return result
	}
	fmt.Printf("  %d new upstream commits\n", newCommits)

	// Step 4: Check for overlapping changes
	overlaps, err := g.CheckOverlaps(target.defaultBranch)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  warning: could not check overlaps: %v\n", err)
	} else {
		result.OverlapWarnings = overlaps
	}

	if upstreamDryRun {
		fmt.Printf("  %s dry-run: would create sync branch and merge %d commits\n",
			style.ArrowPrefix, newCommits)
		return result
	}

	// Step 5: Create sync branch from origin/main
	syncBranch := gitpkg.SyncBranchName()
	result.SyncBranch = syncBranch

	// We need a worktree to perform the merge (bare repos can't merge directly).
	// Use a temporary worktree.
	worktreePath := filepath.Join(target.townRoot, target.rig.Name, ".upstream-sync-work")

	// Clean up any stale worktree from a previous failed run
	_ = g.WorktreeRemove(worktreePath, true)
	_ = os.RemoveAll(worktreePath)
	_ = g.WorktreePrune()

	originRef := "origin/" + target.defaultBranch
	if err := g.WorktreeAddFromRef(worktreePath, syncBranch, originRef); err != nil {
		result.Error = fmt.Errorf("creating sync worktree: %w", err)
		return result
	}
	defer func() {
		// Clean up worktree and sync branch
		_ = g.WorktreeRemove(worktreePath, true)
		_ = os.RemoveAll(worktreePath)
		if !result.MergedToMain {
			// If we didn't merge successfully, clean up the sync branch
			_ = g.DeleteBranch(syncBranch, true)
		}
	}()

	// Step 6: Merge upstream/main into sync branch
	wg := gitpkg.NewGit(worktreePath)
	upstreamRef := "upstream/" + target.defaultBranch
	mergeMsg := fmt.Sprintf("Merge upstream/%s into %s\n\nUpstream: %s\nNew commits: %d",
		target.defaultBranch, target.defaultBranch, target.config.UpstreamURL, newCommits)

	fmt.Printf("  merging upstream/%s...\n", target.defaultBranch)
	if err := wg.MergeNoFF(upstreamRef, mergeMsg); err != nil {
		// Check if it's a conflict
		conflictFiles, cfErr := wg.MergeConflictFiles()
		if cfErr == nil && len(conflictFiles) > 0 {
			result.ConflictFiles = conflictFiles
			_ = wg.AbortMerge()
			fmt.Printf("  %s merge conflicts in: %s\n", style.WarningPrefix,
				strings.Join(conflictFiles, ", "))
			// Sync branch will be cleaned up by the deferred worktree removal
			return result
		}
		result.Error = fmt.Errorf("merging upstream: %w", err)
		return result
	}

	// Step 7: Run tests (unless skipped)
	if !upstreamSkipTests {
		fmt.Printf("  running tests...\n")
		if err := runUpstreamSyncTests(worktreePath); err != nil {
			result.Error = fmt.Errorf("tests failed after merge: %w", err)
			return result
		}
		fmt.Printf("  %s tests passed\n", style.SuccessPrefix)
	}

	// Step 8: Fast-forward origin/main to the sync branch
	fmt.Printf("  fast-forwarding %s...\n", target.defaultBranch)

	// Update the local main ref in the bare repo to point to the sync branch head
	syncHead, err := wg.Rev("HEAD")
	if err != nil {
		result.Error = fmt.Errorf("getting sync branch HEAD: %w", err)
		return result
	}

	if err := g.ResetBranch(target.defaultBranch, syncHead); err != nil {
		result.Error = fmt.Errorf("updating %s ref: %w", target.defaultBranch, err)
		return result
	}

	// Step 9: Push to origin
	fmt.Printf("  pushing to origin...\n")
	if err := g.Push("origin", target.defaultBranch, false); err != nil {
		result.Error = fmt.Errorf("pushing to origin: %w", err)
		return result
	}

	result.MergedToMain = true
	fmt.Printf("  %s synced %d upstream commits\n", style.SuccessPrefix, newCommits)

	// Step 10: Delete sync branch
	_ = g.DeleteBranch(syncBranch, true)

	return result
}

// runUpstreamSyncTests runs the project's test suite in the given directory.
func runUpstreamSyncTests(dir string) error {
	// Detect project type and run appropriate tests
	var testCmd string
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
		testCmd = "go test ./..."
	} else if _, err := os.Stat(filepath.Join(dir, "package.json")); err == nil {
		testCmd = "npm test"
	}
	if testCmd == "" {
		// No recognized test framework — skip silently
		return nil
	}

	cmd := exec.Command("sh", "-c", testCmd) //nolint:gosec // testCmd is hardcoded above
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func runUpstreamStatus(cmd *cobra.Command, args []string) error {
	var rigName string
	if len(args) > 0 {
		rigName = args[0]
	}

	targets, err := rigsWithUpstream(rigName)
	if err != nil {
		return err
	}

	for _, target := range targets {
		bareRepo := target.bareRepoPath()
		g := gitpkg.NewGitWithDir(bareRepo, "")

		// Ensure upstream is configured and fetched
		if err := g.AddUpstreamRemote(target.config.UpstreamURL); err != nil {
			fmt.Fprintf(os.Stderr, "%s %s: error configuring upstream: %v\n", style.ErrorPrefix, target.rig.Name, err)
			continue
		}
		if err := g.FetchUpstream(); err != nil {
			fmt.Fprintf(os.Stderr, "%s %s: error fetching upstream: %v\n", style.ErrorPrefix, target.rig.Name, err)
			continue
		}
		if err := g.Fetch("origin"); err != nil {
			fmt.Fprintf(os.Stderr, "%s %s: error fetching origin: %v\n", style.ErrorPrefix, target.rig.Name, err)
			continue
		}

		fmt.Printf("%s %s (upstream: %s)\n", style.Bold.Render("##"), target.rig.Name, target.config.UpstreamURL)

		// New upstream commits
		newCommits, err := g.UpstreamNewCommits(target.defaultBranch)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  error: %v\n", err)
			continue
		}
		if newCommits == 0 {
			fmt.Printf("  %s up to date with upstream\n", style.SuccessPrefix)
		} else {
			fmt.Printf("  %s %d new upstream commits\n", style.WarningPrefix, newCommits)
		}

		// Local-only commits
		localCommits, err := g.LocalOnlyCommits(target.defaultBranch)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  error listing local commits: %v\n", err)
		} else if len(localCommits) > 0 {
			fmt.Printf("  %d local-only commits (fork customisations):\n", len(localCommits))
			limit := len(localCommits)
			if limit > 10 {
				limit = 10
			}
			for _, c := range localCommits[:limit] {
				fmt.Printf("    %s\n", c)
			}
			if len(localCommits) > 10 {
				fmt.Printf("    ... and %d more\n", len(localCommits)-10)
			}
		}

		// Overlap warnings
		if newCommits > 0 {
			overlaps, err := g.CheckOverlaps(target.defaultBranch)
			if err != nil {
				fmt.Fprintf(os.Stderr, "  warning: could not check overlaps: %v\n", err)
			} else if len(overlaps) > 0 {
				fmt.Printf("  %s %d local commits overlap with upstream changes:\n",
					style.WarningPrefix, len(overlaps))
				for _, w := range overlaps {
					fmt.Printf("    %s %s — files: %s\n",
						w.LocalCommit, w.LocalSubject,
						strings.Join(w.OverlappingFiles, ", "))
				}
			}
		}
		fmt.Println()
	}

	return nil
}
