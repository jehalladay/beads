package main

import "testing"

// TestNormalizeReopenReason is the teeth for beads-5rix3: a whitespace-only
// `bd reopen --reason` must collapse to "" (not provided) so it is NOT stored
// as a blank reopen comment / blank Reopened event payload and does not print
// an empty "Reopened X: " suffix — mirroring the beads-in93a close-reason
// semantics and the defer/comment/note siblings. A genuine reason must be
// returned VERBATIM (no trim) to preserve its formatting.
//
// Pure unit test — no cgo / embedded dolt needed.
func TestNormalizeReopenReason(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"spaces_only", "   ", ""},
		{"tabs_and_newlines", "\t\n  \n", ""},
		{"genuine_reason_verbatim", "reverting the fix", "reverting the fix"},
		{"genuine_reason_with_surrounding_space_preserved", "  keep my spacing  ", "  keep my spacing  "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeReopenReason(tc.in); got != tc.want {
				t.Errorf("normalizeReopenReason(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
