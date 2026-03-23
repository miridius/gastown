package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/steveyegge/gastown/internal/sanitize"
)

var (
	sanitizeJSON  bool
	sanitizeTitle bool
	sanitizeBody  bool
)

var sanitizeCmd = &cobra.Command{
	Use:     "sanitize [text]",
	GroupID: GroupWork,
	Short:   "Sanitize external content for safe agent consumption",
	Long: `Sanitize external content (GitHub issues, emails, webhooks) by stripping
prompt injection patterns before content enters agent context.

Reads from stdin if no text argument is provided.

Detected patterns:
  - System prompt overrides (<system>, <instructions>, etc.)
  - Role manipulation ("ignore previous instructions", etc.)
  - Command injection ("run the following command", etc.)
  - Data exfiltration attempts
  - Hidden content (zero-width characters, HTML comments)

Examples:
  echo "Issue body" | gt sanitize
  echo "Issue body" | gt sanitize --json
  gt sanitize --title "Issue Title"
  gt sanitize "Some text to check"`,
	RunE: runSanitize,
}

func init() {
	rootCmd.AddCommand(sanitizeCmd)
	sanitizeCmd.Flags().BoolVar(&sanitizeJSON, "json", false, "Output as JSON with metadata")
	sanitizeCmd.Flags().BoolVar(&sanitizeTitle, "title", false, "Sanitize as title (stricter: no newlines, length limit)")
	sanitizeCmd.Flags().BoolVar(&sanitizeBody, "body", false, "Sanitize as body (truncates at 10000 chars)")
}

func runSanitize(cmd *cobra.Command, args []string) error {
	var input string

	if len(args) > 0 {
		input = strings.Join(args, " ")
	} else {
		// Read from stdin
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf("reading stdin: %w", err)
		}
		input = string(data)
	}

	if input == "" {
		return fmt.Errorf("no input provided")
	}

	var result *sanitize.Result
	switch {
	case sanitizeTitle:
		result = sanitize.ForAgentTitle(input)
	case sanitizeBody:
		result = sanitize.ForAgentBody(input)
	default:
		result = sanitize.ForAgent(input)
	}

	if sanitizeJSON {
		out := map[string]any{
			"content":      result.Content,
			"clean":        result.IsClean(),
			"flags":        result.Flags,
			"replacements": result.Replacements,
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}

	// Plain text output
	fmt.Print(result.Content)

	if !result.IsClean() {
		fmt.Fprintf(os.Stderr, "\n[sanitize] %d pattern(s) replaced, flags: %s\n",
			result.Replacements, strings.Join(result.Flags, ", "))
	}

	return nil
}
