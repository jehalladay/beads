package main

import "github.com/steveyegge/beads/internal/types"

// issueTypeFilterValue converts a raw --type flag value into the canonical
// IssueType used to build an IssueFilter, expanding aliases (feat->feature,
// enhancement->feature, ...) and case-folding built-in names via
// IssueType.Normalize().
//
// This is the shared chokepoint every command that filters by --type must route
// through (beads-brxo). Several commands (count/search/wisp/lint/migrate) built
// the filter from the RAW flag — `types.IssueType(flag)` with no Normalize — so
// `--type feat` produced IssueType("feat"), which matches nothing stored as the
// canonical "feature", silently dropping the whole feature population (rc=0).
// list/ready/update expand correctly; these commands diverged. Routing all
// sites through one helper means a new command can't reintroduce the bug by
// forgetting to normalize.
//
// The value is only normalized, never rejected: an unknown type normalizes to
// itself and simply matches nothing, preserving each command's existing
// no-error-on-empty-result contract.
func issueTypeFilterValue(flag string) types.IssueType {
	return types.IssueType(flag).Normalize()
}
