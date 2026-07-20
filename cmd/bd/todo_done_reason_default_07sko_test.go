package main

import "testing"

// TestTodoDoneReasonOrDefault is the teeth for beads-07sko: a whitespace-only
// `bd todo done --reason` must fall through to the "Completed" default (not be
// stored as a blank close reason), while a genuine reason is kept VERBATIM.
// Mirrors bd close's TrimSpace guard (in93a) and bd mol squash --summary
// (au0rt) — the override-a-default axis of the stored-blank-reason class.
//
// Pure unit test — no cgo / embedded dolt needed.
func TestTodoDoneReasonOrDefault(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", "Completed"},
		{"spaces_only", "   ", "Completed"},
		{"tabs_and_newlines", "\t\n  \n", "Completed"},
		{"genuine_reason", "Shipped the fix", "Shipped the fix"},
		{"genuine_reason_with_surrounding_space", "  keep my spacing  ", "  keep my spacing  "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := todoDoneReasonOrDefault(tc.in); got != tc.want {
				t.Errorf("todoDoneReasonOrDefault(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
