// Package git provides upstream sync operations for fork workflows.
package git

import (
	"fmt"
	"strings"
	"time"
)

// UpstreamSyncResult contains the outcome of an upstream sync operation.
type UpstreamSyncResult struct {
	// RigName is the rig that was synced.
	RigName string

	// UpstreamURL is the upstream remote URL.
	UpstreamURL string

	// NewCommits is the number of new upstream commits merged.
	NewCommits int

	// SyncBranch is the name of the sync branch created (if any).
	SyncBranch string

	// MergedToMain indicates whether the sync was fast-forwarded to main.
	MergedToMain bool

	// ConflictFiles lists files with merge conflicts (if any).
	ConflictFiles []string

	// OverlapWarnings lists local commits that overlap with upstream changes.
	OverlapWarnings []OverlapWarning

	// Error is set if the sync failed.
	Error error
}

// OverlapWarning indicates a local commit that touches the same files as upstream changes.
type OverlapWarning struct {
	// LocalCommit is the local-only commit hash (short).
	LocalCommit string

	// LocalSubject is the commit subject line.
	LocalSubject string

	// OverlappingFiles are files changed by both local and upstream.
	OverlappingFiles []string
}

// SyncBranchName returns a dated sync branch name with time to avoid collisions.
func SyncBranchName() string {
	return fmt.Sprintf("upstream-sync/%s", time.Now().Format("2006-01-02-150405"))
}

// UpstreamNewCommits returns the number of new commits on upstream/main
// that are not on origin/main. Returns 0 if upstream is not configured.
// Both remotes must be fetched before calling this.
func (g *Git) UpstreamNewCommits(defaultBranch string) (int, error) {
	has, err := g.HasUpstreamRemote()
	if err != nil {
		return 0, err
	}
	if !has {
		return 0, fmt.Errorf("no upstream remote configured")
	}

	originRef := "origin/" + defaultBranch
	upstreamRef := "upstream/" + defaultBranch

	out, err := g.run("rev-list", "--count", originRef+".."+upstreamRef)
	if err != nil {
		return 0, fmt.Errorf("counting upstream commits: %w", err)
	}

	var count int
	if _, err := fmt.Sscanf(strings.TrimSpace(out), "%d", &count); err != nil {
		return 0, fmt.Errorf("parsing commit count: %w", err)
	}
	return count, nil
}

// LocalOnlyCommits returns commits on origin/main that are not on upstream/main.
// These represent local fork customisations.
func (g *Git) LocalOnlyCommits(defaultBranch string) ([]string, error) {
	upstreamRef := "upstream/" + defaultBranch
	originRef := "origin/" + defaultBranch

	out, err := g.run("log", "--oneline", upstreamRef+".."+originRef)
	if err != nil {
		return nil, fmt.Errorf("listing local-only commits: %w", err)
	}
	if out == "" {
		return nil, nil
	}
	return strings.Split(out, "\n"), nil
}

// FilesChangedInRange returns the list of files changed between two refs.
func (g *Git) FilesChangedInRange(from, to string) ([]string, error) {
	out, err := g.run("diff", "--name-only", from+".."+to)
	if err != nil {
		return nil, err
	}
	if out == "" {
		return nil, nil
	}
	return strings.Split(out, "\n"), nil
}

// FilesChangedInCommit returns the list of files changed in a single commit.
func (g *Git) FilesChangedInCommit(ref string) ([]string, error) {
	out, err := g.run("diff-tree", "--no-commit-id", "--name-only", "-r", ref)
	if err != nil {
		return nil, err
	}
	if out == "" {
		return nil, nil
	}
	return strings.Split(out, "\n"), nil
}

// CheckOverlaps compares local-only commits against upstream changes
// and returns warnings where both sides touched the same files.
func (g *Git) CheckOverlaps(defaultBranch string) ([]OverlapWarning, error) {
	originRef := "origin/" + defaultBranch
	upstreamRef := "upstream/" + defaultBranch

	// Get files changed upstream since the fork point
	mergeBase, err := g.run("merge-base", originRef, upstreamRef)
	if err != nil {
		return nil, fmt.Errorf("finding merge base: %w", err)
	}

	upstreamFiles, err := g.FilesChangedInRange(mergeBase, upstreamRef)
	if err != nil {
		return nil, fmt.Errorf("listing upstream changes: %w", err)
	}
	if len(upstreamFiles) == 0 {
		return nil, nil
	}

	upstreamSet := make(map[string]bool, len(upstreamFiles))
	for _, f := range upstreamFiles {
		upstreamSet[f] = true
	}

	// Get local-only commits
	localCommits, err := g.LocalOnlyCommits(defaultBranch)
	if err != nil {
		return nil, err
	}

	var warnings []OverlapWarning
	for _, line := range localCommits {
		parts := strings.SplitN(line, " ", 2)
		if len(parts) < 2 {
			continue
		}
		hash := parts[0]
		subject := parts[1]

		localFiles, err := g.FilesChangedInCommit(hash)
		if err != nil {
			continue // Skip commits we can't inspect
		}

		var overlapping []string
		for _, f := range localFiles {
			if upstreamSet[f] {
				overlapping = append(overlapping, f)
			}
		}

		if len(overlapping) > 0 {
			warnings = append(warnings, OverlapWarning{
				LocalCommit:      hash,
				LocalSubject:     subject,
				OverlappingFiles: overlapping,
			})
		}
	}

	return warnings, nil
}

// MergeConflictFiles returns the list of unmerged files after a failed merge.
func (g *Git) MergeConflictFiles() ([]string, error) {
	out, err := g.run("diff", "--name-only", "--diff-filter=U")
	if err != nil {
		return nil, err
	}
	if out == "" {
		return nil, nil
	}
	return strings.Split(out, "\n"), nil
}
