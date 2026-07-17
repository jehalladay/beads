package tracker

import (
	"strings"
	"testing"
)

// TestValidateSyncState covers beads-jvx: the --state flag on tracker sync
// commands (bd notion/jira/linear sync) is documented as "open, closed, or all"
// but historically an invalid value silently fell through to "match all" (a
// false-broad filter with rc=0), the silent-accept-invalid class. ValidateSyncState
// rejects unknowns with a clear, valid-values-listing error like bd's other
// enum validators (priority/status/type).
func TestValidateSyncState(t *testing.T) {
	t.Run("valid values accepted", func(t *testing.T) {
		for _, v := range []string{"open", "closed", "all"} {
			if err := ValidateSyncState(v); err != nil {
				t.Errorf("ValidateSyncState(%q) = %v, want nil", v, err)
			}
		}
	})

	t.Run("empty is accepted (defaults to all)", func(t *testing.T) {
		if err := ValidateSyncState(""); err != nil {
			t.Errorf("ValidateSyncState(\"\") = %v, want nil", err)
		}
	})

	t.Run("case-insensitive and trims whitespace", func(t *testing.T) {
		for _, v := range []string{"OPEN", " Closed ", "ALL"} {
			if err := ValidateSyncState(v); err != nil {
				t.Errorf("ValidateSyncState(%q) = %v, want nil", v, err)
			}
		}
	})

	t.Run("invalid value rejected, not silently accepted", func(t *testing.T) {
		for _, v := range []string{"opne", "bogus", "opened", "done", "resolved"} {
			err := ValidateSyncState(v)
			if err == nil {
				t.Errorf("ValidateSyncState(%q) = nil, want error (silent-accept-invalid)", v)
				continue
			}
			// Error must be actionable: name the bad value and list valid ones.
			msg := err.Error()
			if !strings.Contains(msg, v) {
				t.Errorf("error for %q does not echo the bad value: %q", v, msg)
			}
			for _, valid := range []string{"open", "closed", "all"} {
				if !strings.Contains(msg, valid) {
					t.Errorf("error for %q does not list valid value %q: %q", v, valid, msg)
				}
			}
		}
	})
}
