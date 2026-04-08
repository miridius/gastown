package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/steveyegge/gastown/internal/beads"
	"github.com/steveyegge/gastown/internal/config"
	"github.com/steveyegge/gastown/internal/git"
	"github.com/steveyegge/gastown/internal/rig"
	"github.com/steveyegge/gastown/internal/style"
	"github.com/steveyegge/gastown/internal/workspace"
)

var (
	pushNoReview bool
	pushIssue    string
)

var pushCmd = &cobra.Command{
	Use:     "push",
	GroupID: GroupWork,
	Short:   "Push changes (routes through review for review-enabled rigs)",
	Long: `Push changes to the remote repository.

For review-enabled rigs (review.enabled in settings/config.json):
  1. Creates a branch: crew/<worker>/<slug>@<timestamp>
  2. Pushes the branch to origin
  3. Creates an MR bead with gt:merge-request + meerkat:review labels
  4. MR appears in meerkat's review queue for human review

For non-review rigs:
  1. Pushes directly to the default branch (current behavior)

This is the sanctioned push mechanism for crew workers. It replaces
raw "git push" and ensures work in review-enabled rigs flows through
the meerkat review queue.

Examples:
  gt push                    # Auto-detect: review or direct push
  gt push --no-review        # Bypass review (emergencies only)
  gt push --issue gt-abc     # Associate MR with a specific issue`,
	RunE: runPush,
}

func init() {
	pushCmd.Flags().BoolVar(&pushNoReview, "no-review", false, "Bypass review even in review-enabled rigs (emergencies only)")
	pushCmd.Flags().StringVar(&pushIssue, "issue", "", "Associate the MR with a specific issue ID")
	rootCmd.AddCommand(pushCmd)
}

func runPush(cmd *cobra.Command, args []string) error {
	// Find workspace
	townRoot, err := workspace.FindFromCwdOrError()
	if err != nil {
		return fmt.Errorf("not in a Gas Town workspace: %w", err)
	}

	// Find current rig
	rigName, _, err := findCurrentRig(townRoot)
	if err != nil {
		return err
	}

	// Determine working directory (handle shell alias cwd == townRoot case)
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getting current directory: %w", err)
	}
	if cwd == townRoot {
		if crewName := os.Getenv("GT_CREW"); crewName != "" && rigName != "" {
			crewClone := filepath.Join(townRoot, rigName, "crew", crewName)
			if _, err := os.Stat(crewClone); err == nil {
				cwd = crewClone
			}
		}
	}

	g := git.NewGit(cwd)

	// Check for uncommitted changes
	hasChanges, err := g.HasUncommittedChanges()
	if err != nil {
		return fmt.Errorf("checking git status: %w", err)
	}
	if hasChanges {
		return fmt.Errorf("uncommitted changes detected — commit before pushing")
	}

	// Load rig settings to check review config
	rigPath := filepath.Join(townRoot, rigName)
	settingsPath := filepath.Join(rigPath, "settings", "config.json")
	var reviewEnabled bool
	if rigSettings, err := config.LoadRigSettings(settingsPath); err == nil {
		reviewEnabled = rigSettings.IsReviewEnabled()
	}

	// Override review if --no-review flag set
	if pushNoReview {
		if reviewEnabled {
			fmt.Printf("%s Review bypass requested (--no-review)\n", style.WarningPrefix)
		}
		reviewEnabled = false
	}

	if !reviewEnabled {
		return pushDirect(g, rigPath, rigName)
	}

	return pushForReview(g, cwd, townRoot, rigName, rigPath)
}

// pushDirect pushes directly to the default branch (non-review rigs).
func pushDirect(g *git.Git, rigPath, rigName string) error {
	defaultBranch := "main"
	if rigCfg, err := rig.LoadRigConfig(rigPath); err == nil && rigCfg.DefaultBranch != "" {
		defaultBranch = rigCfg.DefaultBranch
	}

	fmt.Printf("Pushing to %s/%s...\n", rigName, defaultBranch)
	if err := g.Push("origin", defaultBranch, false); err != nil {
		return fmt.Errorf("git push failed: %w", err)
	}

	fmt.Printf("%s Pushed to %s\n", style.Bold.Render("✓"), defaultBranch)
	return nil
}

// pushForReview creates a review branch, pushes it, and creates an MR bead.
func pushForReview(g *git.Git, cwd, townRoot, rigName, rigPath string) error {
	// Determine crew worker name
	worker := detectCrewWorker(townRoot, rigName)
	if worker == "" {
		return fmt.Errorf("cannot determine crew worker identity (set GT_CREW or run from a crew worktree)")
	}

	// Get default branch for comparison
	defaultBranch := "main"
	if rigCfg, err := rig.LoadRigConfig(rigPath); err == nil && rigCfg.DefaultBranch != "" {
		defaultBranch = rigCfg.DefaultBranch
	}

	// Check current branch — if already on a crew/ branch, just push it
	currentBranch, err := g.CurrentBranch()
	if err != nil {
		return fmt.Errorf("getting current branch: %w", err)
	}

	var branch string
	if currentBranch != defaultBranch && currentBranch != "master" {
		// Already on a non-default branch — use it directly
		branch = currentBranch
	} else {
		// On default branch — create a crew push branch
		slug := generateBranchSlug(cwd)
		timestamp := time.Now().Format("20060102T1504")
		branch = fmt.Sprintf("crew/%s/%s@%s", worker, slug, timestamp)

		if err := g.CheckoutNewBranch(branch, "HEAD"); err != nil {
			return fmt.Errorf("creating branch %s: %w", branch, err)
		}
	}

	// Push the branch
	fmt.Printf("Pushing %s for review...\n", branch)
	if err := g.Push("origin", branch, false); err != nil {
		return fmt.Errorf("git push failed: %w", err)
	}

	// Create MR bead
	bd := beads.New(cwd)

	issueID := pushIssue
	if issueID == "" {
		// Try to extract from branch name
		info := parseBranchName(branch)
		issueID = info.Issue
	}

	title := fmt.Sprintf("Merge: crew/%s", worker)
	if issueID != "" {
		title = fmt.Sprintf("Merge: %s (crew/%s)", issueID, worker)
	}

	commitSHA, _ := g.Rev("HEAD")

	description := fmt.Sprintf("branch: %s\ntarget: %s\nrig: %s\nworker: crew/%s",
		branch, defaultBranch, rigName, worker)
	if issueID != "" {
		description += fmt.Sprintf("\nsource_issue: %s", issueID)
	}
	if commitSHA != "" {
		description += fmt.Sprintf("\ncommit_sha: %s", commitSHA)
	}

	// Check for existing MR (idempotency)
	var mrIssue *beads.Issue
	if commitSHA != "" {
		if existing, err := bd.FindMRForBranchAndSHA(branch, commitSHA); err == nil && existing != nil {
			mrIssue = existing
			fmt.Printf("%s MR already exists (idempotent)\n", style.Bold.Render("✓"))
		}
	}

	if mrIssue == nil {
		labels := []string{"gt:merge-request", "meerkat:review"}

		var err error
		mrIssue, err = bd.Create(beads.CreateOptions{
			Title:       title,
			Labels:      labels,
			Priority:    2,
			Description: description,
			Ephemeral:   true,
		})
		if err != nil {
			return fmt.Errorf("creating merge request bead: %w", err)
		}

		// Back-link source issue if available
		if issueID != "" {
			comment := fmt.Sprintf("MR created: %s (via gt push)", mrIssue.ID)
			if _, err := bd.Run("comments", "add", issueID, comment); err != nil {
				style.PrintWarning("could not back-link source issue %s to MR %s: %v", issueID, mrIssue.ID, err)
			}
		}
	}

	// Success output
	fmt.Printf("%s Submitted for review\n", style.Bold.Render("✓"))
	fmt.Printf("  MR ID:   %s\n", style.Bold.Render(mrIssue.ID))
	fmt.Printf("  Branch:  %s\n", branch)
	fmt.Printf("  Target:  %s\n", defaultBranch)
	fmt.Printf("  Worker:  crew/%s\n", worker)
	if issueID != "" {
		fmt.Printf("  Issue:   %s\n", issueID)
	}
	fmt.Printf("\n%s\n", style.Dim.Render("Submitted for review in meerkat. Refinery will process after approval."))

	return nil
}

// detectCrewWorker determines the crew worker name from environment or cwd.
func detectCrewWorker(townRoot, rigName string) string {
	// Check GT_CREW env var first
	if crew := os.Getenv("GT_CREW"); crew != "" {
		return crew
	}

	// Try to detect from cwd path (e.g., /gt/meerkat/crew/alan/meerkat -> "alan")
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	crewDir := filepath.Join(townRoot, rigName, "crew")
	rel, err := filepath.Rel(crewDir, cwd)
	if err != nil || strings.HasPrefix(rel, "..") {
		return ""
	}
	parts := strings.Split(rel, string(filepath.Separator))
	if len(parts) > 0 && parts[0] != "" && parts[0] != "." {
		return parts[0]
	}
	return ""
}

// slugChars matches non-alphanumeric characters for branch slug generation.
var slugChars = regexp.MustCompile(`[^a-z0-9]+`)

// generateBranchSlug creates a short slug from the most recent commit message.
func generateBranchSlug(cwd string) string {
	// Get the latest commit message subject line
	cmd := exec.Command("git", "log", "-1", "--format=%s")
	cmd.Dir = cwd
	out, err := cmd.Output()
	if err != nil || len(out) == 0 {
		return "push"
	}

	subject := strings.ToLower(strings.TrimSpace(string(out)))

	// Remove common prefixes (feat:, fix:, chore:, etc.)
	if idx := strings.Index(subject, ":"); idx > 0 && idx < 15 {
		subject = strings.TrimSpace(subject[idx+1:])
	}

	// Remove issue references in parens, e.g. "(gt-abc)"
	if idx := strings.LastIndex(subject, "("); idx > 0 {
		subject = strings.TrimSpace(subject[:idx])
	}

	slug := slugChars.ReplaceAllString(subject, "-")
	slug = strings.Trim(slug, "-")

	// Truncate to reasonable length
	if len(slug) > 40 {
		slug = slug[:40]
		// Don't end on a hyphen
		slug = strings.TrimRight(slug, "-")
	}

	if slug == "" {
		return "push"
	}
	return slug
}
