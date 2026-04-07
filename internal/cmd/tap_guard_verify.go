package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

var tapGuardVerifyBeforeDoneCmd = &cobra.Command{
	Use:   "verify-before-done",
	Short: "Block gt done without empirical verification evidence",
	Long: `Block gt done if the polecat has not recorded empirical verification evidence.

This guard enforces the mandatory empirical verification step: polecats must
exercise their work and record evidence before submitting via gt done.

Evidence is recorded by writing to .gt/verification-evidence in the worktree.
The file must exist and be non-empty.

The guard is bypassed when:
  - Not running as a polecat (GT_POLECAT not set)
  - gt done is called with --cleanup-status (no-changes workflow)
  - gt done is called with --status=ESCALATED (escalation workflow)

Exit codes:
  0 - Operation allowed
  2 - Operation BLOCKED (no verification evidence found)`,
	RunE: runTapGuardVerifyBeforeDone,
}

func init() {
	tapGuardCmd.AddCommand(tapGuardVerifyBeforeDoneCmd)
}

func runTapGuardVerifyBeforeDone(cmd *cobra.Command, args []string) error {
	// Only applies to polecats
	if os.Getenv("GT_POLECAT") == "" {
		return nil
	}

	// Read hook input from stdin (Claude Code protocol)
	input, err := io.ReadAll(os.Stdin)
	if err != nil {
		return nil // fail open
	}

	command := extractCommand(input)

	// Allow bypass workflows that don't require verification
	if strings.Contains(command, "--cleanup-status") ||
		strings.Contains(command, "--status=ESCALATED") ||
		strings.Contains(command, "--status ESCALATED") {
		return nil
	}

	// Find the verification evidence file
	evidencePath := findVerificationEvidence()
	if evidencePath != "" {
		return nil // evidence found, allow
	}

	// No evidence — block
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "╔══════════════════════════════════════════════════════════════════╗")
	fmt.Fprintln(os.Stderr, "║  ❌ gt done BLOCKED — NO VERIFICATION EVIDENCE                   ║")
	fmt.Fprintln(os.Stderr, "╠══════════════════════════════════════════════════════════════════╣")
	fmt.Fprintln(os.Stderr, "║  You must empirically verify your work before submitting.       ║")
	fmt.Fprintln(os.Stderr, "║                                                                  ║")
	fmt.Fprintln(os.Stderr, "║  Record evidence:                                                ║")
	fmt.Fprintln(os.Stderr, "║    mkdir -p .gt && cat > .gt/verification-evidence <<'EOF'       ║")
	fmt.Fprintln(os.Stderr, "║    ## Verification                                               ║")
	fmt.Fprintln(os.Stderr, "║    $ <command you ran>                                           ║")
	fmt.Fprintln(os.Stderr, "║    <output proving it works>                                     ║")
	fmt.Fprintln(os.Stderr, "║    EOF                                                           ║")
	fmt.Fprintln(os.Stderr, "║                                                                  ║")
	fmt.Fprintln(os.Stderr, "║  No-changes workflow:                                            ║")
	fmt.Fprintln(os.Stderr, "║    gt done --cleanup-status clean                                ║")
	fmt.Fprintln(os.Stderr, "╚══════════════════════════════════════════════════════════════════╝")
	fmt.Fprintln(os.Stderr, "")
	return NewSilentExit(2)
}

// findVerificationEvidence looks for the verification evidence file in the
// polecat worktree. Returns the path if found (non-empty file), empty string otherwise.
func findVerificationEvidence() string {
	// Try CWD first
	cwd, err := os.Getwd()
	if err == nil {
		if p := checkEvidenceAt(cwd); p != "" {
			return p
		}
	}

	// Try GT_POLECAT_PATH env var (set by Gas Town session management)
	polecatPath := os.Getenv("GT_POLECAT_PATH")
	if polecatPath != "" {
		if p := checkEvidenceAt(polecatPath); p != "" {
			return p
		}
	}

	return ""
}

// checkEvidenceAt checks if a non-empty verification-evidence file exists at the given base path.
func checkEvidenceAt(basePath string) string {
	evidencePath := filepath.Join(basePath, ".gt", "verification-evidence")
	info, err := os.Stat(evidencePath)
	if err != nil {
		return ""
	}
	if info.Size() == 0 {
		return ""
	}
	return evidencePath
}
