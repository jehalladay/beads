package main

import "testing"

// TestTreeLimitIgnored covers the decision behind the beads-3dr5 hint: a
// positive --limit that the --parent tree view's child count exceeds was
// effectively ignored (the tree renders in full to avoid orphaning children),
// so the CLI should warn instead of silently dropping the flag. The stderr
// write itself is terminal-gated (like printTruncationHint), so the testable
// unit is this pure decision function.
func TestTreeLimitIgnored(t *testing.T) {
	cases := []struct {
		name           string
		effectiveLimit int
		childCount     int
		want           bool
	}{
		{"limit_exceeded_warns", 5, 8, true},
		{"limit_exactly_met_no_warn", 5, 5, false},
		{"limit_not_reached_no_warn", 5, 3, false},
		{"zero_limit_means_all_no_warn", 0, 100, false},
		{"negative_limit_no_warn", -1, 100, false},
		{"no_children_no_warn", 5, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := treeLimitIgnored(tc.effectiveLimit, tc.childCount); got != tc.want {
				t.Errorf("treeLimitIgnored(%d, %d) = %v, want %v",
					tc.effectiveLimit, tc.childCount, got, tc.want)
			}
		})
	}
}
