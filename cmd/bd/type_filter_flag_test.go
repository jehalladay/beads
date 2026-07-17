package main

import (
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestIssueTypeFilterValueNormalizes pins beads-brxo: the shared --type filter
// helper must expand aliases and case-fold built-ins so `--type feat` builds a
// filter for the canonical "feature" (not the raw "feat", which matches nothing
// stored). Before the fix, count/search/wisp/lint/migrate built the filter from
// the raw flag and silently dropped the whole feature population.
func TestIssueTypeFilterValueNormalizes(t *testing.T) {
	cases := []struct {
		in   string
		want types.IssueType
	}{
		{"feat", types.TypeFeature},        // alias -> canonical
		{"enhancement", types.TypeFeature}, // alias -> canonical
		{"feature", types.TypeFeature},     // already canonical
		{"FEATURE", types.TypeFeature},     // case-fold built-in (brxo + 7wrj theme)
		{"Bug", types.TypeBug},             // case-fold built-in
		{"task", types.TypeTask},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			if got := issueTypeFilterValue(tc.in); got != tc.want {
				t.Errorf("issueTypeFilterValue(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestIssueTypeFilterValueUnknownUnchanged confirms an unknown type normalizes
// to itself (lower-cased if a built-in, else verbatim) — the helper never
// rejects, preserving the commands' no-error-on-empty-result contract.
func TestIssueTypeFilterValueUnknownUnchanged(t *testing.T) {
	// A genuinely unknown type is returned unchanged (Normalize only folds when
	// the lowercased form is a built-in alias/type).
	if got := issueTypeFilterValue("totally-made-up"); got != types.IssueType("totally-made-up") {
		t.Errorf("issueTypeFilterValue(unknown) = %q, want it unchanged", got)
	}
}
