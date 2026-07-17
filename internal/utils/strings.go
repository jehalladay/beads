package utils

import (
	"strings"

	"github.com/steveyegge/beads/internal/types"
)

// NormalizeIssueType expands type aliases to their canonical forms on the
// list/ready/update-filter path. It delegates to the SINGLE canonical alias
// table (types.IssueTypeAliases via IssueType.Normalize) so the filter path and
// the create path can never diverge again: previously this map and
// IssueType.Normalize had DISJOINT aliases, so `bd create -t investigation`
// stored "spike" while `bd list -t investigation` looked for "investigation"
// and silently missed it (beads-9k6o).
//
// For example: "mr" -> "merge-request", "feat" -> "feature", "mol" -> "molecule",
// "investigation" -> "spike". Returns the input unchanged if it's neither an
// alias nor a canonical built-in.
func NormalizeIssueType(t string) string {
	return string(types.IssueType(t).Normalize())
}

// NormalizeLabels trims whitespace, removes empty strings, and deduplicates labels
// while preserving order.
func NormalizeLabels(ss []string) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}
