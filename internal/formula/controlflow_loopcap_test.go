package formula

import (
	"strings"
	"testing"
)

// TestLoopIterationCap verifies beads-r7bv: count/range loop expansion must be
// bounded by DefaultMaxLoopIterations so a formula can't eagerly materialize
// millions/billions of step bodies (OOM/DoS at cook time), mirroring the
// existing DefaultMaxExpansionDepth guard for recursive template expansion.
func TestLoopIterationCap(t *testing.T) {
	body := []*Step{{ID: "a", Title: "A"}}

	t.Run("count over cap is rejected at validation", func(t *testing.T) {
		steps := []*Step{{
			ID:    "big",
			Title: "Big",
			Loop:  &LoopSpec{Count: DefaultMaxLoopIterations + 1, Body: body},
		}}
		_, err := ApplyLoops(steps)
		if err == nil {
			t.Fatalf("expected an error for count > DefaultMaxLoopIterations, got nil")
		}
		if !strings.Contains(err.Error(), "count") {
			t.Errorf("expected error to mention 'count', got: %v", err)
		}
	})

	t.Run("count at cap is allowed", func(t *testing.T) {
		steps := []*Step{{
			ID:    "atcap",
			Title: "AtCap",
			Loop:  &LoopSpec{Count: 3, Body: body},
		}}
		if _, err := ApplyLoops(steps); err != nil {
			t.Errorf("count within cap should not error, got: %v", err)
		}
	})

	t.Run("range span over cap is rejected during expansion", func(t *testing.T) {
		steps := []*Step{{
			ID:    "bigrange",
			Title: "BigRange",
			Loop:  &LoopSpec{Range: "1..1000000", Var: "i", Body: body},
		}}
		_, err := ApplyLoops(steps)
		if err == nil {
			t.Fatalf("expected an error for a range span > DefaultMaxLoopIterations, got nil")
		}
		if !strings.Contains(err.Error(), "iteration") && !strings.Contains(err.Error(), "range") {
			t.Errorf("expected error to mention 'iteration' or 'range', got: %v", err)
		}
	})

	t.Run("small range is allowed", func(t *testing.T) {
		steps := []*Step{{
			ID:    "smallrange",
			Title: "SmallRange",
			Loop:  &LoopSpec{Range: "1..3", Var: "i", Body: body},
		}}
		if _, err := ApplyLoops(steps); err != nil {
			t.Errorf("small range should not error, got: %v", err)
		}
	})
}
