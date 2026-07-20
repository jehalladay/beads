package formula

import "testing"

// TestValidate_GateTimerTimeoutPositivity is the teeth for beads-luk9k: a
// formula step whose timer gate declares a zero or negative timeout must fail
// Validate() (at cook/lint), mirroring the bd gate create guard (beads-cx0eu).
// A positive timeout still validates, and non-timer gates are unaffected.
func TestValidate_GateTimerTimeoutPositivity(t *testing.T) {
	base := func(gate *Gate) *Formula {
		return &Formula{
			Formula: "mol-gate",
			Version: 1,
			Type:    TypeWorkflow,
			Steps: []*Step{
				{ID: "step1", Title: "Step 1", Gate: gate},
			},
		}
	}

	tests := []struct {
		name    string
		gate    *Gate
		wantErr bool
	}{
		{"zero timer timeout", &Gate{Type: "timer", Timeout: "0s"}, true},
		{"negative timer timeout", &Gate{Type: "timer", Timeout: "-5m"}, true},
		{"positive timer timeout", &Gate{Type: "timer", Timeout: "5m"}, false},
		{"timer no timeout (owned by other guard, not this one)", &Gate{Type: "timer"}, false},
		{"zero timeout on non-timer gate is not this guard's concern", &Gate{Type: "human", Timeout: "0s"}, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := base(tc.gate).Validate()
			if tc.wantErr && err == nil {
				t.Fatalf("Validate() = nil, want error for gate %+v", tc.gate)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("Validate() = %v, want nil for gate %+v", err, tc.gate)
			}
		})
	}
}

// TestValidate_GateTimerTimeoutPositivity_Children ensures the guard also
// covers gates on child steps, since cook's collectSteps recursion pours child
// gates the same way it pours top-level step gates.
func TestValidate_GateTimerTimeoutPositivity_Children(t *testing.T) {
	f := &Formula{
		Formula: "mol-gate-child",
		Version: 1,
		Type:    TypeWorkflow,
		Steps: []*Step{
			{
				ID:    "parent",
				Title: "Parent",
				Children: []*Step{
					{ID: "child", Title: "Child", Gate: &Gate{Type: "timer", Timeout: "0s"}},
				},
			},
		},
	}
	if err := f.Validate(); err == nil {
		t.Fatal("Validate() = nil, want error for zero-timeout timer gate on a child step")
	}
}
