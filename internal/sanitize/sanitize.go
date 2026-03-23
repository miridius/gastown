// Package sanitize provides content filtering for external input that will be
// read by AI agents. It strips prompt injection patterns, role manipulation
// attempts, and instruction overrides before content enters agent context.
//
// This is a security boundary: all external content (GitHub issues, emails,
// webhooks) must pass through SanitizeForAgent before any agent reads it.
package sanitize

import (
	"fmt"
	"regexp"
	"strings"
)

// Pattern categories for prompt injection detection.
var (
	// systemPromptPatterns match attempts to override system instructions.
	systemPromptPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)<\s*system\s*>`),
		regexp.MustCompile(`(?i)<\s*/\s*system\s*>`),
		regexp.MustCompile(`(?i)<\s*system[-_]?prompt\s*>`),
		regexp.MustCompile(`(?i)<\s*/\s*system[-_]?prompt\s*>`),
		regexp.MustCompile(`(?i)<\s*instructions?\s*>`),
		regexp.MustCompile(`(?i)<\s*/\s*instructions?\s*>`),
		regexp.MustCompile(`(?i)<\s*system[-_]?reminder\s*>`),
		regexp.MustCompile(`(?i)<\s*/\s*system[-_]?reminder\s*>`),
	}

	// roleManipulationPatterns match attempts to change agent identity or role.
	roleManipulationPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)you\s+are\s+now\s+`),
		regexp.MustCompile(`(?i)ignore\s+(all\s+)?(previous|prior|above)\s+(instructions?|prompts?|rules?)`),
		regexp.MustCompile(`(?i)forget\s+(all\s+)?(previous|prior|above)\s+(instructions?|prompts?|context)`),
		regexp.MustCompile(`(?i)disregard\s+(all\s+)?(previous|prior|above)\s+(instructions?|prompts?|rules?)`),
		regexp.MustCompile(`(?i)new\s+instructions?:\s*`),
		regexp.MustCompile(`(?i)override\s+(all\s+)?instructions?`),
		regexp.MustCompile(`(?i)from\s+now\s+on,?\s+(you\s+)?(are|will|must|should)`),
		regexp.MustCompile(`(?i)act\s+as\s+(if\s+you\s+are\s+|though\s+you\s+are\s+)?a\s+`),
		regexp.MustCompile(`(?i)pretend\s+(you\s+are|to\s+be)\s+`),
		regexp.MustCompile(`(?i)switch\s+to\s+.*\s+mode`),
	}

	// commandInjectionPatterns match attempts to inject tool/API calls.
	// Note: "run the following command" is NOT matched because it's common in
	// legitimate bug reports ("I ran the following command and got an error").
	// We only match synthetic API/tool injection tags.
	commandInjectionPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)<\s*tool_use\s*>`),
		regexp.MustCompile(`(?i)<\s*/\s*tool_use\s*>`),
		regexp.MustCompile(`(?i)<\s*function_call\s*>`),
		regexp.MustCompile(`(?i)<\s*/\s*function_call\s*>`),
		regexp.MustCompile(`(?i)<\s*antml:`),
	}

	// dataExfiltrationPatterns match attempts to leak data through external channels.
	dataExfiltrationPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)send\s+(the|all|my|this)\s+.{0,30}\s+to\s+`),
		regexp.MustCompile(`(?i)email\s+(the|all|my|this)\s+.{0,30}\s+to\s+`),
		regexp.MustCompile(`(?i)post\s+(the|all|my|this)\s+.{0,30}\s+to\s+`),
		regexp.MustCompile(`(?i)upload\s+(the|all|my|this)\s+.{0,30}\s+to\s+`),
		regexp.MustCompile(`(?i)curl\s+.*\s+-d\s+`),
	}

	// hiddenContentPatterns match attempts to hide malicious content.
	hiddenContentPatterns = []*regexp.Regexp{
		// Zero-width characters used for invisible instruction injection
		regexp.MustCompile(`[\x{200B}\x{200C}\x{200D}\x{FEFF}\x{2060}]+`),
		// HTML comments that may contain hidden instructions
		regexp.MustCompile(`<!--[\s\S]*?-->`),
	}
)

// Result contains the sanitized content and metadata about what was removed.
type Result struct {
	// Content is the sanitized text, safe for agent consumption.
	Content string

	// Flags lists the categories of suspicious patterns that were found.
	// Empty if the content was clean.
	Flags []string

	// Replacements is the count of patterns that were replaced.
	Replacements int
}

// IsClean returns true if no suspicious patterns were found.
func (r *Result) IsClean() bool {
	return r.Replacements == 0
}

// ForAgent sanitizes external content for safe consumption by AI agents.
// It replaces detected prompt injection patterns with redaction markers
// while preserving the rest of the content for human readability.
func ForAgent(content string) *Result {
	r := &Result{Content: content}
	flagSet := make(map[string]bool)

	// Remove hidden content first (zero-width chars, HTML comments)
	for _, pat := range hiddenContentPatterns {
		if pat.MatchString(r.Content) {
			r.Content = pat.ReplaceAllString(r.Content, "")
			r.Replacements++
			flagSet["hidden-content"] = true
		}
	}

	// Replace system prompt overrides
	for _, pat := range systemPromptPatterns {
		if pat.MatchString(r.Content) {
			r.Content = pat.ReplaceAllString(r.Content, "[REDACTED:system-prompt-override]")
			r.Replacements++
			flagSet["system-prompt-override"] = true
		}
	}

	// Replace role manipulation attempts
	for _, pat := range roleManipulationPatterns {
		if pat.MatchString(r.Content) {
			r.Content = pat.ReplaceAllString(r.Content, "[REDACTED:role-manipulation]")
			r.Replacements++
			flagSet["role-manipulation"] = true
		}
	}

	// Replace command injection attempts
	for _, pat := range commandInjectionPatterns {
		if pat.MatchString(r.Content) {
			r.Content = pat.ReplaceAllString(r.Content, "[REDACTED:command-injection]")
			r.Replacements++
			flagSet["command-injection"] = true
		}
	}

	// Replace data exfiltration attempts
	for _, pat := range dataExfiltrationPatterns {
		if pat.MatchString(r.Content) {
			r.Content = pat.ReplaceAllString(r.Content, "[REDACTED:data-exfiltration]")
			r.Replacements++
			flagSet["data-exfiltration"] = true
		}
	}

	for flag := range flagSet {
		r.Flags = append(r.Flags, flag)
	}

	return r
}

// ForAgentTitle sanitizes a title/subject line. Titles should be short and
// descriptive — anything that looks like an instruction is suspicious.
func ForAgentTitle(title string) *Result {
	r := ForAgent(title)

	// Additional title-specific checks: titles shouldn't contain newlines
	if strings.Contains(r.Content, "\n") {
		r.Content = strings.ReplaceAll(r.Content, "\n", " ")
		r.Replacements++
		if !containsFlag(r.Flags, "format-manipulation") {
			r.Flags = append(r.Flags, "format-manipulation")
		}
	}

	// Truncate extremely long titles (likely injection attempts)
	const maxTitleLen = 200
	if len(r.Content) > maxTitleLen {
		r.Content = r.Content[:maxTitleLen] + "..."
		r.Replacements++
		if !containsFlag(r.Flags, "length-exceeded") {
			r.Flags = append(r.Flags, "length-exceeded")
		}
	}

	return r
}

// ForAgentBody sanitizes a body/description. Bodies can be longer but are
// truncated at a maximum length to prevent context stuffing attacks.
func ForAgentBody(body string) *Result {
	r := ForAgent(body)

	const maxBodyLen = 10000
	if len(r.Content) > maxBodyLen {
		r.Content = r.Content[:maxBodyLen] + "\n\n[truncated — original length: " + fmt.Sprintf("%d", len(body)) + " chars]"
		r.Replacements++
		if !containsFlag(r.Flags, "length-exceeded") {
			r.Flags = append(r.Flags, "length-exceeded")
		}
	}

	return r
}

func containsFlag(flags []string, flag string) bool {
	for _, f := range flags {
		if f == flag {
			return true
		}
	}
	return false
}
