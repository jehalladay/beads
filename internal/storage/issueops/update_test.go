package issueops

import (
	"strings"
	"testing"
)

// TestValidatePriorityUpdate verifies the boundary guard that rejects
// out-of-range or malformed priority values before they reach the SQL write
// path. This is the root-cause guard for the batch/proxied update paths that
// do not route through validation.ValidatePriority (beads-r06.11).
func TestValidatePriorityUpdate(t *testing.T) {
	tests := []struct {
		name      string
		updates   map[string]interface{}
		wantError bool
		wantNorm  interface{} // expected normalized value when no error (nil = don't check)
	}{
		// No priority key: nothing to validate.
		{name: "absent", updates: map[string]interface{}{"title": "x"}, wantError: false},

		// Valid int values (CLI flag parsing supplies int).
		{name: "int 0", updates: map[string]interface{}{"priority": 0}, wantError: false, wantNorm: 0},
		{name: "int 2", updates: map[string]interface{}{"priority": 2}, wantError: false, wantNorm: 2},
		{name: "int 4", updates: map[string]interface{}{"priority": 4}, wantError: false, wantNorm: 4},

		// Out-of-range ints (the silent-corruption path this guard closes).
		{name: "int 5", updates: map[string]interface{}{"priority": 5}, wantError: true},
		{name: "int 999", updates: map[string]interface{}{"priority": 999}, wantError: true},
		{name: "int -1", updates: map[string]interface{}{"priority": -1}, wantError: true},

		// JSON-decoded plans deliver float64; integral values in range are OK.
		{name: "float64 3", updates: map[string]interface{}{"priority": float64(3)}, wantError: false, wantNorm: 3},
		{name: "float64 out of range", updates: map[string]interface{}{"priority": float64(7)}, wantError: true},
		{name: "float64 non-integral", updates: map[string]interface{}{"priority": 2.5}, wantError: true},

		// int64 (some decoders/paths) in range.
		{name: "int64 1", updates: map[string]interface{}{"priority": int64(1)}, wantError: false, wantNorm: 1},

		// Wrong type entirely.
		{name: "string", updates: map[string]interface{}{"priority": "high"}, wantError: true},
		{name: "nil", updates: map[string]interface{}{"priority": nil}, wantError: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validatePriorityUpdate(tt.updates)
			if (err != nil) != tt.wantError {
				t.Fatalf("validatePriorityUpdate(%v) error = %v, wantError %v", tt.updates, err, tt.wantError)
			}
			if err != nil {
				// Error message must name the field and the 0-4 range so the CLI
				// surfaces an actionable message.
				msg := err.Error()
				if !strings.Contains(msg, "priority") {
					t.Errorf("error message %q does not mention 'priority'", msg)
				}
				return
			}
			if tt.wantNorm != nil {
				got := tt.updates["priority"]
				if got != tt.wantNorm {
					t.Errorf("normalized priority = %v (%T), want %v (%T)", got, got, tt.wantNorm, tt.wantNorm)
				}
			}
		})
	}
}
