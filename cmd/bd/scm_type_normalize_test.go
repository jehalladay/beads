package main

import (
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// beads-15vj: the SCM sync --type/--exclude-type filters must route through the
// canonical normalizer (issueTypeFilterValue) so aliases and mixed case resolve
// to the stored canonical IssueType. engine.shouldSync compares by exact ==, so
// a raw or ToLower-only value silently syncs nothing (--type) or fails open
// (--exclude-type leaks the type into the external tracker).

func TestGitlabParseTypeListNormalizesAliases(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []types.IssueType
	}{
		{"alias mr expands", "mr", []types.IssueType{types.IssueType("merge-request")}},
		{"alias feat expands", "feat", []types.IssueType{types.TypeFeature}},
		{"mixed case folds", "Bug", []types.IssueType{types.TypeBug}},
		{"comma list mixed", "mr,feat,epic", []types.IssueType{types.IssueType("merge-request"), types.TypeFeature, types.TypeEpic}},
		{"whitespace trimmed then normalized", " mr , feat ", []types.IssueType{types.IssueType("merge-request"), types.TypeFeature}},
		{"empty yields nil", "", nil},
		{"unknown preserved as-is", "widget", []types.IssueType{types.IssueType("widget")}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseTypeList(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("parseTypeList(%q) = %v, want %v", tc.in, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("parseTypeList(%q)[%d] = %q, want %q", tc.in, i, got[i], tc.want[i])
				}
			}
		})
	}
}

// issueTypeFilterValue is the shared chokepoint the linear sync path now uses
// for both --type and --exclude-type; assert the alias/case contract holds for
// the SCM-relevant aliases (regression guard for beads-15vj on the linear leg).
func TestIssueTypeFilterValueSCMAliases(t *testing.T) {
	cases := map[string]types.IssueType{
		"mr":      types.IssueType("merge-request"),
		"feat":    types.TypeFeature,
		"Feature": types.TypeFeature, // built-in case-folds
		"widget":  types.IssueType("widget"),
	}
	for in, want := range cases {
		if got := issueTypeFilterValue(in); got != want {
			t.Errorf("issueTypeFilterValue(%q) = %q, want %q", in, got, want)
		}
	}
}
