package main

import "testing"

// TestCustomTitleProvided is the teeth for beads-2itry: a whitespace-only
// `bd mol bond --as "   "` must be treated as NOT provided (false), so both the
// store leg (bondProtoProto) and the dry-run display (printMolBondDryRun) fall
// through to the computed "Compound: A + B" title instead of clobbering it with
// blank whitespace. The pre-fix `customTitle != ""` check let a whitespace-only
// value through (it is != ""), so the compound proto's Title became "   ".
//
// Mirrors the override-a-default whitespace class: dolt commit -m/--message
// (beads-by9ph doltCommitMessageProvided), mol squash --summary
// (beads-au0rt squashSummaryProvided), todo done --reason (beads-07sko).
// A genuine title is accepted as-provided (true) — its content is used VERBATIM.
// Pure unit test — no cgo / embedded dolt needed.
func TestCustomTitleProvided(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"empty", "", false},
		{"spaces_only", "   ", false},
		{"tab_only", "\t", false},
		{"newline_only", "\n", false},
		{"mixed_whitespace", " \t\n ", false},
		{"genuine_title", "My Compound Proto", true},
		{"genuine_title_with_surrounding_space", "  keep my spacing  ", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := customTitleProvided(tc.in); got != tc.want {
				t.Errorf("customTitleProvided(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}
