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

// TestIssueTypeFilterValuesNormalizesExcludes pins beads-asls: the slice helper
// used to build `wisp gc --exclude-type` protections must normalize every
// value, so the documented `--exclude-type mol` resolves to the stored
// canonical "molecule" and actually protects molecules from a destructive GC.
// Before the fix, wisp gc built the exclude filter from raw IssueType(flag) —
// IssueType("mol") never matched stored "molecule", so the protection failed
// OPEN and the molecules the user asked to keep were deleted.
func TestIssueTypeFilterValuesNormalizesExcludes(t *testing.T) {
	got := issueTypeFilterValues([]string{"mol", "ENHANCEMENT", "Task"})
	want := []types.IssueType{types.TypeMolecule, types.TypeFeature, types.TypeTask}
	if len(got) != len(want) {
		t.Fatalf("issueTypeFilterValues len = %d (%v), want %d (%v)", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("issueTypeFilterValues[%d] = %q, want %q (alias/case not normalized)", i, got[i], want[i])
		}
	}
}

// TestIssueTypeFilterValuesSplitsAndTrims confirms comma-separated and
// whitespace-padded values are split/trimmed (matching the sibling ready/list
// exclude-type loops), and empties are skipped so no "" exclude leaks through.
func TestIssueTypeFilterValuesSplitsAndTrims(t *testing.T) {
	got := issueTypeFilterValues([]string{" mol , bug ", "", "  ", "task"})
	want := []types.IssueType{types.TypeMolecule, types.TypeBug, types.TypeTask}
	if len(got) != len(want) {
		t.Fatalf("issueTypeFilterValues len = %d (%v), want %d (%v)", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("issueTypeFilterValues[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestIssueTypeFilterValuesEmpty confirms an empty/nil input yields nil (no
// spurious empty-string exclude that would match nothing / everything).
func TestIssueTypeFilterValuesEmpty(t *testing.T) {
	if got := issueTypeFilterValues(nil); got != nil {
		t.Errorf("issueTypeFilterValues(nil) = %v, want nil", got)
	}
}
