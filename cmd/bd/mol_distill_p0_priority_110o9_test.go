package main

import (
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestDistillPreservesP0Priority_110o9 pins the priority round-trip of
// subgraphToFormula. Priority 0 is P0/critical (a VALID priority, NOT "unset")
// — the earlier `if issue.Priority > 0` guard treated P0 as unset, leaving the
// distilled step's Priority nil. Pour then defaults a nil priority to 2
// (cook.go), silently DOWNGRADING a P0 step to P2 (beads-110o9).
//
// MUTATION-VERIFY: restore `if issue.Priority > 0` in subgraphToFormula and the
// P0 subtest goes RED (step.Priority == nil), proving the teeth are load-bearing.
func TestDistillPreservesP0Priority_110o9(t *testing.T) {
	tests := []struct {
		name       string
		priority   int
		wantCopied bool // true => step.Priority non-nil and == priority
	}{
		{name: "p0_critical_preserved", priority: 0, wantCopied: true},
		{name: "p1_preserved", priority: 1, wantCopied: true},
		{name: "p2_default_omitted", priority: 2, wantCopied: false},
		{name: "p3_preserved", priority: 3, wantCopied: true},
		{name: "p4_preserved", priority: 4, wantCopied: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			subgraph := &TemplateSubgraph{
				Root: &types.Issue{ID: "root", Title: "Root epic"},
				Issues: []*types.Issue{
					{ID: "root", Title: "Root epic"},
					{ID: "step", Title: "critical step", Priority: tt.priority},
				},
			}

			f := subgraphToFormula(subgraph, "prio", nil)
			if f == nil {
				t.Fatal("subgraphToFormula returned nil")
			}

			var found bool
			var got *int
			for _, s := range f.Steps {
				if s.Title == "critical step" {
					found = true
					got = s.Priority
				}
			}
			if !found {
				t.Fatalf("emitted formula has no 'critical step' (steps=%d)", len(f.Steps))
			}

			if tt.wantCopied {
				if got == nil {
					t.Fatalf("priority %d dropped: step.Priority == nil (pour would default it to %d)",
						tt.priority, distillPourDefaultPriority)
				}
				if *got != tt.priority {
					t.Errorf("step.Priority = %d, want %d", *got, tt.priority)
				}
			} else if got != nil {
				t.Errorf("default priority %d should be omitted (nil) to keep the formula clean, got %d",
					tt.priority, *got)
			}
		})
	}
}
