package jira

import (
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// beads-famu: StatusToBeads's reverse lookup must be DETERMINISTIC on a
// non-injective statusMap (two beads statuses mapping to the same Jira name).
// Before the fix it iterated the map directly, so Go's randomized map order
// made it return an arbitrary beads status that varied across runs. The fix
// iterates beadsStatus keys sorted.
func TestStatusToBeadsDeterministicOnNonInjectiveMap(t *testing.T) {
	t.Parallel()
	// Both "open" and "in_progress" map to Jira "Active" (non-injective).
	m := &jiraFieldMapper{statusMap: map[string]string{
		string(types.StatusOpen):       "Active",
		string(types.StatusInProgress): "Active",
		string(types.StatusClosed):     "Done",
	}}

	first := m.StatusToBeads("Active")
	// Sorted beadsStatus keys: "in_progress" < "open" → in_progress wins.
	if first != types.StatusInProgress {
		t.Fatalf("StatusToBeads(\"Active\") = %v, want deterministic %v (sorted-first of tied statuses)", first, types.StatusInProgress)
	}
	for i := 0; i < 200; i++ {
		if got := m.StatusToBeads("Active"); got != first {
			t.Fatalf("StatusToBeads non-deterministic: iteration %d got %v, first got %v", i, got, first)
		}
	}
}
