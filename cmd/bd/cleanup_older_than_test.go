package main

import "testing"

// TestValidateOlderThan pins beads-93px: `bd admin cleanup --older-than N` must
// reject a negative N. The age bound is only applied when N > 0, so a negative
// value would silently skip the bound and widen a scoped delete to "all closed
// issues" — a destructive scope-widen on durable data. N == 0 is the documented
// "all closed" default and must stay valid; positive N is valid. This mirrors
// the read-only twin `bd stale --days` (rejects days < 1), the asymmetry that
// proved cleanup's missing guard was a real bug, not by-design.
func TestValidateOlderThan(t *testing.T) {
	cases := []struct {
		name    string
		days    int
		wantErr bool
	}{
		{"negative rejected (the bug)", -1, true},
		{"large negative rejected", -365, true},
		{"zero is the documented all-closed default", 0, false},
		{"positive is valid", 30, false},
		{"one is valid", 1, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateOlderThan(tc.days)
			if tc.wantErr && err == nil {
				t.Errorf("validateOlderThan(%d) = nil, want an error (negative would silently widen to delete-all-closed)", tc.days)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("validateOlderThan(%d) = %v, want nil", tc.days, err)
			}
		})
	}
}
