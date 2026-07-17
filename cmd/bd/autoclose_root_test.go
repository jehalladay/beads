package main

import (
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// beads-q46r: hermetic test for shouldAutoCloseCompletedRoot (close.go), a pure
// predicate over *types.Issue (verified 0% + no test refs).
func TestShouldAutoCloseCompletedRoot(t *testing.T) {
	cases := []struct {
		name string
		iss  *types.Issue
		want bool
	}{
		{"nil → false", nil, false},
		{"molecule → true", &types.Issue{IssueType: types.TypeMolecule}, true},
		{"ephemeral → true", &types.Issue{IssueType: types.TypeTask, Ephemeral: true}, true},
		{"non-epic non-molecule → false", &types.Issue{IssueType: types.TypeTask}, false},
		{"bug → false", &types.Issue{IssueType: types.TypeBug}, false},
		{"plain epic (no template label) → false", &types.Issue{IssueType: types.TypeEpic}, false},
		{
			"epic with template label → true",
			&types.Issue{IssueType: types.TypeEpic, Labels: []string{"other", BeadsTemplateLabel}},
			true,
		},
		{
			"epic with only non-template labels → false",
			&types.Issue{IssueType: types.TypeEpic, Labels: []string{"urgent", "backend"}},
			false,
		},
	}
	for _, c := range cases {
		if got := shouldAutoCloseCompletedRoot(c.iss); got != c.want {
			t.Errorf("%s: shouldAutoCloseCompletedRoot = %v, want %v", c.name, got, c.want)
		}
	}
}
