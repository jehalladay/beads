package main

import "testing"

// TestSquashSummaryProvided is the teeth for beads-au0rt: a whitespace-only
// `bd mol squash --summary` must be treated as NOT provided (false), so
// squashMolecule falls through to generateDigest() instead of overwriting the
// permanent digest's Description with blank whitespace. A genuine summary must
// be accepted as-provided (true) — its content is later used VERBATIM.
//
// Pure unit test — no cgo / embedded dolt needed.
func TestSquashSummaryProvided(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"empty", "", false},
		{"spaces_only", "   ", false},
		{"tabs_and_newlines", "\t\n  \n", false},
		{"genuine_summary", "Agent-generated summary of work done", true},
		{"genuine_summary_with_surrounding_space", "  keep my spacing  ", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := squashSummaryProvided(tc.in); got != tc.want {
				t.Errorf("squashSummaryProvided(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}
