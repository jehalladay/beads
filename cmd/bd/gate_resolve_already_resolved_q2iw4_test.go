package main

import (
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestGateAlreadyResolved is the teeth for beads-q2iw4: `bd gate resolve` on an
// ALREADY-resolved (closed) gate must be detected so both entry points can emit
// an idempotent no-op notice instead of a false "✓ Gate resolved" + a fresh
// reason that contradicts the stored state (store.CloseIssue is a no-op on an
// already-closed issue, so the 2nd resolve's reason is silently discarded).
//
// Mirrors bd close's already-closed guard (close.go:145, beads-dr3) and gate's
// own add-waiter idempotent notice. Pure unit test — no cgo / embedded dolt.
func TestGateAlreadyResolved(t *testing.T) {
	cases := []struct {
		name  string
		issue *types.Issue
		want  bool
	}{
		{"nil issue", nil, false},
		{"open gate", &types.Issue{Status: types.StatusOpen, IssueType: "gate"}, false},
		{"blocked gate", &types.Issue{Status: types.StatusBlocked, IssueType: "gate"}, false},
		{"in-progress gate", &types.Issue{Status: types.StatusInProgress, IssueType: "gate"}, false},
		{"closed gate (already resolved)", &types.Issue{Status: types.StatusClosed, IssueType: "gate", CloseReason: "approved"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := gateAlreadyResolved(tc.issue); got != tc.want {
				t.Errorf("gateAlreadyResolved(%+v) = %v, want %v", tc.issue, got, tc.want)
			}
		})
	}
}
