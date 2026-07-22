//go:build cgo

package main

import (
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestDuplicatesHelpDocumentsActualMergeTargetOrder is a doc/code drift guard for
// beads-wqqb1: the `bd duplicates` --help long text documented a stale 2-criterion
// merge-target rule (reference count, then lexicographic ID) while chooseMergeTarget
// actually ranks by a 3-criterion rule with STRUCTURAL WEIGHT as the primary key
// (dependents*3 + depends-on), then text references, then lexicographic ID. This test
// pins the docs to the implementation: the Long text MUST name all three criteria in
// the order the code applies them, and a behavioral cross-check confirms the code
// honors that documented order (weight outranks references).
func TestDuplicatesHelpDocumentsActualMergeTargetOrder(t *testing.T) {
	long := strings.ToLower(duplicatesCmd.Long)

	idxWeight := strings.Index(long, "structural weight")
	idxRefs := strings.Index(long, "reference count")
	idxLex := strings.Index(long, "lexicographically smallest id")

	if idxWeight < 0 {
		t.Fatalf("duplicates --help must document structural weight as the primary merge-target criterion (chooseMergeTarget weights dependents 3x); not found in Long:\n%s", duplicatesCmd.Long)
	}
	if idxRefs < 0 {
		t.Fatalf("duplicates --help must document text reference count as a merge-target criterion; not found in Long:\n%s", duplicatesCmd.Long)
	}
	if idxLex < 0 {
		t.Fatalf("duplicates --help must document the lexicographic-ID tiebreaker; not found in Long:\n%s", duplicatesCmd.Long)
	}

	// The documented order must match chooseMergeTarget's precedence:
	// structural weight, then text references, then lexicographic ID.
	if !(idxWeight < idxRefs && idxRefs < idxLex) {
		t.Errorf("duplicates --help documents merge-target criteria out of order: want structural weight (%d) < reference count (%d) < lexicographic ID (%d), to match chooseMergeTarget", idxWeight, idxRefs, idxLex)
	}

	// Behavioral cross-check: an issue with higher structural weight but FEWER text
	// references must still win, exactly as the Long text now claims (weight > refs).
	// If either the docs regress to refs-primary OR the code changes to make refs
	// primary, this drift guard fails.
	group := []*types.Issue{
		{ID: "bd-hi-weight", Title: "Task"},
		{ID: "bd-hi-refs", Title: "Task"},
	}
	refCounts := map[string]int{"bd-hi-weight": 0, "bd-hi-refs": 50}
	structuralScores := map[string]*issueScore{
		"bd-hi-weight": {dependentCount: 1, dependsOnCount: 0, textRefs: 0}, // weight = 3
		"bd-hi-refs":   {dependentCount: 0, dependsOnCount: 0, textRefs: 50},
	}
	got := chooseMergeTarget(group, refCounts, structuralScores)
	gotID := "<nil>"
	if got != nil {
		gotID = got.ID
	}
	if gotID != "bd-hi-weight" {
		t.Errorf("chooseMergeTarget does not honor the documented weight>refs precedence: got %s, want bd-hi-weight", gotID)
	}
}
