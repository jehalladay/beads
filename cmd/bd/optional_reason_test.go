package main

import "testing"

// TestNormalizeOptionalReason is the pure-Go teeth for beads-tg1js: a
// whitespace-only optional --reason collapses to "" (not provided) so callers'
// `if reason != ""` guards do not store a dangling blank suffix; a genuine
// reason is returned VERBATIM (no trim) to preserve formatting. Mirrors the
// beads-5rix3 normalizeReopenReason contract.
func TestNormalizeOptionalReason(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"spaces_only", "   ", ""},
		{"tabs_and_newlines", "\t\n  \n", ""},
		{"genuine_reason_verbatim", "no longer applicable", "no longer applicable"},
		{"genuine_reason_surrounding_space_preserved", "  keep spacing  ", "  keep spacing  "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeOptionalReason(tc.in); got != tc.want {
				t.Errorf("normalizeOptionalReason(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
