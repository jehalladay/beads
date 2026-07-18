package main

import (
	"strings"

	"github.com/steveyegge/beads/internal/types"
)

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

// issueTypeFilterValues normalizes a slice of raw --type/--exclude-type flag
// values through issueTypeFilterValue. Commands that accept a repeatable type
// flag (e.g. `wisp gc --exclude-type mol,agent`) must route through this so an
// alias or mixed-case value resolves to the stored canonical type. It also
// splits comma-separated values and trims/skips empties, matching the sibling
// list/ready exclude-type loops.
//
// On a DESTRUCTIVE command an un-normalized exclude fails OPEN: `--exclude-type
// mol` built the raw IssueType("mol"), which never matches the stored canonical
// "molecule", so the molecules the user asked to PROTECT were not excluded and
// got deleted (beads-asls). This is the write-side sibling of the brxo read
// family — same shared table, same chokepoint.
func issueTypeFilterValues(flags []string) []types.IssueType {
	var out []types.IssueType
	for _, raw := range flags {
		for _, t := range strings.Split(raw, ",") {
			t = strings.TrimSpace(t)
			if t == "" {
				continue
			}
			out = append(out, issueTypeFilterValue(t))
		}
	}
	return out
}
