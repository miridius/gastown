package config

import (
	"fmt"
	"strings"
)

// CostTier represents a predefined cost optimization tier for model selection.
type CostTier string

const (
	// TierStandard uses opus for all roles (default, highest quality).
	TierStandard CostTier = "standard"
	// TierEconomy uses sonnet/haiku for patrol roles, keeps opus for workers.
	TierEconomy CostTier = "economy"
	// TierBudget uses haiku/sonnet for patrols, sonnet for workers.
	TierBudget CostTier = "budget"
)

// ValidCostTiers returns all valid tier names.
func ValidCostTiers() []string {
	return []string{string(TierStandard), string(TierEconomy), string(TierBudget)}
}

// IsValidTier checks if a string is a valid cost tier name.
func IsValidTier(tier string) bool {
	switch CostTier(tier) {
	case TierStandard, TierEconomy, TierBudget:
		return true
	default:
		return false
	}
}

// TierManagedRoles is the set of roles whose model selection is managed by cost tiers.
// These are the only roles that ApplyCostTier modifies — any other custom RoleAgents
// entries (e.g., user-defined roles or non-Claude agents for non-tier roles) are preserved.
//
// "boot" and "dog" are utility roles that should always use the cheapest model.
var TierManagedRoles = []string{"mayor", "deacon", "witness", "refinery", "polecat", "crew", "boot", "dog"}

// CostTierRoleAgents returns the role_agents mapping for a given tier.
// All tiers explicitly map every tier-managed role. Standard tier maps all roles
// to empty string (meaning "use default/opus"), while other tiers specify
// Claude model variants. Returns nil if the tier is invalid.
func CostTierRoleAgents(tier CostTier) map[string]string {
	switch tier {
	case TierStandard:
		// Explicit mapping for all managed roles — empty value means "use default"
		// Mayor and polecat are pinned to opus-4-6 (high-judgment and implementation roles)
		// Worker roles (witness, refinery, polecat) use capped variants for CPU isolation
		// Boot and dog are utility roles — always haiku even on standard tier
		return map[string]string{
			"mayor":    "claude-opus-4-6",
			"deacon":   "",
			"witness":  "claude-opus-4-6-capped",
			"refinery": "claude-opus-4-6-capped",
			"polecat":  "claude-opus-4-6-capped",
			"crew":     "",
			"boot":     "claude-haiku",
			"dog":      "claude-haiku",
		}
	case TierEconomy:
		return map[string]string{
			"mayor":    "claude-opus-4-6",
			"deacon":   "claude-haiku",
			"witness":  "claude-sonnet-capped",
			"refinery": "claude-sonnet-capped",
			"polecat":  "claude-opus-4-6-capped",
			"crew":     "", // use default
			"boot":     "claude-haiku",
			"dog":      "claude-haiku",
		}
	case TierBudget:
		return map[string]string{
			"mayor":    "claude-opus-4-6",
			"deacon":   "claude-haiku",
			"witness":  "claude-haiku-capped",
			"refinery": "claude-haiku-capped",
			"polecat":  "claude-opus-4-6-capped",
			"crew":     "claude-sonnet",
			"boot":     "claude-haiku",
			"dog":      "claude-haiku",
		}
	default:
		return nil
	}
}

// CostTierAgents returns the custom agent definitions needed for a given tier.
// These define the claude model presets and their cgroup-wrapped (capped) variants.
// Capped variants use cgroup-wrap to cap CPU per agent, preventing runaway processes
// from saturating all cores.
func CostTierAgents(tier CostTier) map[string]*RuntimeConfig {
	switch tier {
	case TierStandard:
		return map[string]*RuntimeConfig{
			"claude-opus-4-6":        claudeOpus46Preset(),
			"claude-opus-4-6-capped": claudeOpus46CappedPreset(),
			"claude-haiku":           claudeHaikuPreset(),
		}
	case TierEconomy:
		return map[string]*RuntimeConfig{
			"claude-opus-4-6":        claudeOpus46Preset(),
			"claude-opus-4-6-capped": claudeOpus46CappedPreset(),
			"claude-sonnet":          claudeSonnetPreset(),
			"claude-sonnet-capped":   claudeSonnetCappedPreset(),
			"claude-haiku":           claudeHaikuPreset(),
		}
	case TierBudget:
		return map[string]*RuntimeConfig{
			"claude-opus-4-6":        claudeOpus46Preset(),
			"claude-opus-4-6-capped": claudeOpus46CappedPreset(),
			"claude-sonnet":          claudeSonnetPreset(),
			"claude-haiku":           claudeHaikuPreset(),
			"claude-haiku-capped":    claudeHaikuCappedPreset(),
		}
	default:
		return nil
	}
}

// claudeOpus46Preset returns a RuntimeConfig for Claude Opus 4.6.
func claudeOpus46Preset() *RuntimeConfig {
	return &RuntimeConfig{
		Command: "claude",
		Args:    []string{"--dangerously-skip-permissions", "--model", "claude-opus-4-6"},
	}
}

// claudeSonnetPreset returns a RuntimeConfig for Claude Sonnet.
func claudeSonnetPreset() *RuntimeConfig {
	return &RuntimeConfig{
		Command: "claude",
		Args:    []string{"--dangerously-skip-permissions", "--model", "sonnet"},
	}
}

// claudeHaikuPreset returns a RuntimeConfig for Claude Haiku.
func claudeHaikuPreset() *RuntimeConfig {
	return &RuntimeConfig{
		Command: "claude",
		Args:    []string{"--dangerously-skip-permissions", "--model", "haiku"},
	}
}

// claudeOpus46CappedPreset returns a cgroup-wrapped RuntimeConfig for Claude Opus 4.6.
// The cgroup-wrap binary caps CPU usage per agent to prevent resource saturation.
func claudeOpus46CappedPreset() *RuntimeConfig {
	return &RuntimeConfig{
		Command: "cgroup-wrap",
		Args:    []string{"claude", "--dangerously-skip-permissions", "--model", "claude-opus-4-6"},
	}
}

// claudeSonnetCappedPreset returns a cgroup-wrapped RuntimeConfig for Claude Sonnet.
func claudeSonnetCappedPreset() *RuntimeConfig {
	return &RuntimeConfig{
		Command: "cgroup-wrap",
		Args:    []string{"claude", "--dangerously-skip-permissions", "--model", "sonnet"},
	}
}

// claudeHaikuCappedPreset returns a cgroup-wrapped RuntimeConfig for Claude Haiku.
func claudeHaikuCappedPreset() *RuntimeConfig {
	return &RuntimeConfig{
		Command: "cgroup-wrap",
		Args:    []string{"claude", "--dangerously-skip-permissions", "--model", "haiku"},
	}
}

// ApplyCostTier writes the tier's agent and role_agents configuration to town settings.
// Only tier-managed roles are modified — custom RoleAgents entries for non-tier roles
// (or intentional non-Claude overrides) are preserved.
// For standard tier, tier-managed roles are removed from RoleAgents (using defaults)
// and tier-specific agent presets are cleaned up.
func ApplyCostTier(settings *TownSettings, tier CostTier) error {
	roleAgents := CostTierRoleAgents(tier)
	if roleAgents == nil {
		return fmt.Errorf("invalid cost tier: %q (valid: %s)", tier, strings.Join(ValidCostTiers(), ", "))
	}

	agents := CostTierAgents(tier)

	// Ensure RoleAgents map exists
	if settings.RoleAgents == nil {
		settings.RoleAgents = make(map[string]string)
	}

	// Only update tier-managed roles, preserving any custom entries
	for _, role := range TierManagedRoles {
		agentName := roleAgents[role]
		if agentName == "" {
			// Empty means "use default" — remove any tier override for this role
			delete(settings.RoleAgents, role)
		} else {
			settings.RoleAgents[role] = agentName
		}
	}

	// Ensure agents map exists
	if settings.Agents == nil {
		settings.Agents = make(map[string]*RuntimeConfig)
	}

	// Determine which tier-specific agents this tier needs vs. doesn't need
	tierAgentNames := map[string]bool{
		"claude-opus-4-6":        false,
		"claude-opus-4-6-capped": false,
		"claude-sonnet":          false,
		"claude-sonnet-capped":   false,
		"claude-haiku":           false,
		"claude-haiku-capped":    false,
	}
	for name := range agents {
		tierAgentNames[name] = true
	}
	// Add agents this tier needs, remove ones it doesn't
	for name, needed := range tierAgentNames {
		if needed {
			settings.Agents[name] = agents[name]
		} else {
			delete(settings.Agents, name)
		}
	}

	// Track the tier for display purposes
	settings.CostTier = string(tier)

	return nil
}

// GetCurrentTier infers the current cost tier from the settings' RoleAgents.
// Returns the tier name if it matches a known tier exactly, or empty string for custom configs.
// Only tier-managed roles are compared — non-tier custom entries are ignored.
func GetCurrentTier(settings *TownSettings) string {
	// Check informational field first for quick path
	if settings.CostTier != "" && IsValidTier(settings.CostTier) {
		// Verify it still matches the actual config
		expected := CostTierRoleAgents(CostTier(settings.CostTier))
		if tierRolesMatch(settings.RoleAgents, expected) {
			return settings.CostTier
		}
	}

	// Infer from RoleAgents by checking each tier
	for _, tierName := range ValidCostTiers() {
		tier := CostTier(tierName)
		expected := CostTierRoleAgents(tier)
		if tierRolesMatch(settings.RoleAgents, expected) {
			return tierName
		}
	}

	return "" // Custom configuration
}

// tierRolesMatch checks if the actual RoleAgents map matches a tier's expected
// assignments for tier-managed roles only. Non-tier custom entries in actual are ignored.
// An empty or missing value in actual matches an empty expected value (both mean "use default").
func tierRolesMatch(actual, expected map[string]string) bool {
	for _, role := range TierManagedRoles {
		actualVal := actual[role]   // "" if not present
		expectedVal := expected[role] // "" means "use default"
		if actualVal != expectedVal {
			return false
		}
	}
	return true
}

// TierDescription returns a human-readable description of the tier's model assignments.
func TierDescription(tier CostTier) string {
	switch tier {
	case TierStandard:
		return "Mayor uses Opus 4.6, workers (witness/refinery/polecat) use cgroup-capped Opus 4.6"
	case TierEconomy:
		return "Mayor uses Opus 4.6, polecat uses capped Opus 4.6, patrol roles use capped Sonnet/Haiku"
	case TierBudget:
		return "Mayor and polecats use Opus 4.6, patrol roles use Haiku, crew uses Sonnet"
	default:
		return "Unknown tier"
	}
}

// FormatTierRoleTable returns a formatted string showing role→model assignments for a tier.
func FormatTierRoleTable(tier CostTier) string {
	roleAgents := CostTierRoleAgents(tier)
	if roleAgents == nil {
		return ""
	}

	roles := []string{"mayor", "deacon", "witness", "refinery", "polecat", "crew", "boot", "dog"}
	var lines []string
	for _, role := range roles {
		agent := roleAgents[role]
		if agent == "" {
			agent = "(default)"
		}
		lines = append(lines, fmt.Sprintf("  %-10s %s", role+":", agent))
	}
	return strings.Join(lines, "\n")
}
