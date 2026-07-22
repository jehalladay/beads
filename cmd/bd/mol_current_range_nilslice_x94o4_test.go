package main

import (
	"encoding/json"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestFilterStepsByRange_OutOfRangeEmptyNotNil_x94o4 guards beads-x94o4:
// filterStepsByRange must return an EMPTY (non-nil) slice — not nil — when the
// range start is beyond the step count. MoleculeProgress.Steps has no omitempty
// json tag, so a nil Steps marshals to "steps":null in `bd mol current --range
// <past-last-step> --json`, diverging from the default path (which inits Steps
// to []*StepStatus{} in getMoleculeProgress) that emits "steps":[]. Same command
// must not change JSON schema by flag (wgvo1/1sq7f/2llrj nil-slice contract).
//
// The step JSON marshal below is what actually reaches the user via
// outputJSON(molecules): a nil slice -> null, an empty slice -> [].
func TestFilterStepsByRange_OutOfRangeEmptyNotNil_x94o4(t *testing.T) {
	steps := []*StepStatus{
		{Issue: &types.Issue{ID: "s1"}, Status: "done"},
		{Issue: &types.Issue{ID: "s2"}, Status: "current"},
		{Issue: &types.Issue{ID: "s3"}, Status: "pending"},
	}

	tests := []struct {
		name       string
		start, end int
		wantLen    int
		wantJSON   string // how the result marshals as the no-omitempty Steps field
	}{
		{"start_past_end_returns_empty_not_nil", 5, 6, 0, "[]"},
		{"start_equals_len_plus_one_returns_empty", 4, 4, 0, "[]"},
		{"in_range_unchanged", 1, 2, 2, ""},
		{"end_clamped_to_len", 2, 99, 2, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := filterStepsByRange(steps, tt.start, tt.end)
			if len(got) != tt.wantLen {
				t.Fatalf("filterStepsByRange(%d,%d) len=%d, want %d", tt.start, tt.end, len(got), tt.wantLen)
			}
			if tt.wantLen == 0 {
				// The critical assertion: out-of-range must not be nil, so the
				// no-omitempty Steps field marshals to [] not null.
				if got == nil {
					t.Fatalf("filterStepsByRange(%d,%d) returned nil — want empty non-nil slice (beads-x94o4: nil -> steps:null in --json)", tt.start, tt.end)
				}
				data, err := json.Marshal(got)
				if err != nil {
					t.Fatalf("json.Marshal: %v", err)
				}
				if string(data) != tt.wantJSON {
					t.Errorf("empty range marshaled to %q, want %q (steps:null divergence)", string(data), tt.wantJSON)
				}
			}
		})
	}
}
