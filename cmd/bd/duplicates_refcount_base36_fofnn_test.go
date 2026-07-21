package main

import (
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestCountReferences_Base36LetterSuffix_fofnn proves countReferences counts
// text mentions of a BASE36 (letter-bearing) issue id, not only digits-only
// sequential ids.
//
// beads-fofnn: countReferences (cmd/bd/duplicates.go) matched refs with
//
//	\b[a-zA-Z][-a-zA-Z0-9]*-\d+\b
//
// — the `\d+` suffix is DIGITS ONLY. Real bd ids are base36 hashes (e.g.
// op-16f), so a text mention of a letter-suffix id never matched (the trailing
// `f` blocks `\d+\b`), and refCounts[id] stayed 0. That silently disables the
// textRefs tiebreaker in chooseMergeTarget: when two duplicates tie on
// structural weight, `bd duplicates --auto-merge` should keep the
// more-referenced one, but with all counts at 0 it falls through to the
// lexicographic-id tiebreaker and can discard the more-connected duplicate.
// Fix mirrors the rename-prefix repair path's alnum suffix (rename_prefix.go:355).
//
// ⚠️ Why this shipped green: duplicates_test.go exercises chooseMergeTarget with
// PRE-BUILT refCounts maps, never the regex, and its fixtures use numeric ids
// (bd-1/bd-2). These teeth call countReferences directly with a letter-suffix ref.
//
// MUTATION-VERIFY: revert idPattern to `-\d+\b` and the base36 subtests go RED —
// the letter-suffix ref op-16f is counted 0 instead of 1.
func TestCountReferences_Base36LetterSuffix_fofnn(t *testing.T) {
	// A base36 letter-suffix id (op-16f) mentioned in another issue's text
	// fields must be counted. The numeric control (op-42) proves the pattern
	// still matches sequential ids (no regression).
	issues := []*types.Issue{
		{ID: "op-16f", Title: "target", Status: types.StatusOpen},
		{ID: "op-42", Title: "numeric target", Status: types.StatusOpen},
		{
			ID:          "op-ref",
			Title:       "referencing issue",
			Status:      types.StatusOpen,
			Description: "blocked on op-16f before we ship",
			Design:      "see op-16f and op-42 for context",
			Notes:       "duplicate of op-16f",
		},
	}

	counts := countReferences(issues)

	// base36 letter-suffix id: mentioned in Description, Design, Notes = 3.
	if got := counts["op-16f"]; got != 3 {
		t.Errorf("beads-fofnn: countReferences miscounted base36 letter-suffix ref op-16f\n"+
			"got %d, want 3 (Description+Design+Notes); the digits-only `-\\d+` regex skipped the base36 suffix", got)
	}

	// numeric id: mentioned once in Design = 1 (regression guard).
	if got := counts["op-42"]; got != 1 {
		t.Errorf("beads-fofnn: countReferences miscounted numeric ref op-42\n"+
			"got %d, want 1 (Design); the fix must stay backward-compatible with sequential ids", got)
	}
}
