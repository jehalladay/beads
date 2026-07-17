package main

import "testing"

// TestNormalizeAssignee verifies that the "none" unassigned sentinel maps to ""
// (unassign), matching query/list semantics where assignee=none means unassigned
// (beads-19g). Real names and "" pass through unchanged.
func TestNormalizeAssignee(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"none", ""},        // sentinel -> unassign (beads-19g)
		{"None", ""},        // case-insensitive
		{"NONE", ""},        // case-insensitive
		{" none ", ""},      // trimmed
		{"", ""},            // already unassign
		{"alice", "alice"},  // real name unchanged
		{"beads/crew/x", "beads/crew/x"}, // slash-path unchanged
		{"nonesuch", "nonesuch"},          // not the sentinel
	}
	for _, c := range cases {
		if got := normalizeAssignee(c.in); got != c.want {
			t.Errorf("normalizeAssignee(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
