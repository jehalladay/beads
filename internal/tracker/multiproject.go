package tracker

import "strings"

// ParseCommaSeparated splits a comma-separated string into trimmed, non-empty elements.
// Used by trackers that support multiple project/team IDs via config.
func ParseCommaSeparated(s string) []string {
	parts := strings.Split(s, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		trimmed := strings.TrimSpace(p)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

// DeduplicateStrings returns a slice with duplicates removed, preserving order.
func DeduplicateStrings(ss []string) []string {
	seen := make(map[string]bool, len(ss))
	result := make([]string, 0, len(ss))
	for _, s := range ss {
		if !seen[s] {
			seen[s] = true
			result = append(result, s)
		}
	}
	return result
}

// ResolveProjectIDs resolves the effective project/team IDs from config sources.
// It checks plural first, then singular, and deduplicates the result.
// The cliOverride takes highest precedence when non-empty.
func ResolveProjectIDs(cliOverride []string, pluralVal, singularVal string) []string {
	if len(cliOverride) > 0 {
		return DeduplicateStrings(cliOverride)
	}
	if pluralVal != "" {
		parsed := ParseCommaSeparated(pluralVal)
		if len(parsed) > 0 {
			return DeduplicateStrings(parsed)
		}
	}
	if singularVal != "" {
		return []string{singularVal}
	}
	// beads-jxel: return a non-nil empty slice, not nil. Callers embed this
	// result directly in --json status output (linear "team_ids", jira
	// "jira_projects", ado "projects"); a nil slice marshals to JSON null,
	// forcing consumers to special-case null vs []. All callers gate on
	// len(...)>0, so the empty slice is behaviorally identical while giving a
	// stable array contract on the unconfigured path.
	return []string{}
}
