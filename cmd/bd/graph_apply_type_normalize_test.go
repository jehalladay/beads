package main

import (
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestValidateGraphApplyPlan_NormalizesTypeAliases verifies that `bd create
// --graph` accepts documented type aliases the same way `bd create -t <type>`
// does (beads-h3k5). Before the fix validateGraphApplyPlan checked the RAW
// node.Type against IsValidWithCustom, so a documented alias like "feat"/"mol"
// was rejected ("invalid type") — an asymmetry with plain bd create, which
// normalizes feat->feature.
func TestValidateGraphApplyPlan_NormalizesTypeAliases(t *testing.T) {
	// No custom types configured: only built-ins + aliases should pass.
	for _, alias := range []string{"feat", "enhancement", "mol", "dec", "investigation", "BUG"} {
		plan := &GraphApplyPlan{Nodes: []GraphApplyNode{
			{Key: "n1", Title: "aliased node", Type: alias},
		}}
		if err := validateGraphApplyPlan(plan, nil); err != nil {
			t.Errorf("validateGraphApplyPlan should accept documented alias %q (should normalize): %v", alias, err)
		}
	}
}

// TestValidateGraphApplyPlan_StillRejectsUnknownType verifies the fix does not
// weaken validation: a genuinely unknown, non-alias, non-custom type is still
// rejected.
func TestValidateGraphApplyPlan_StillRejectsUnknownType(t *testing.T) {
	plan := &GraphApplyPlan{Nodes: []GraphApplyNode{
		{Key: "n1", Title: "bad node", Type: "frobnicate"},
	}}
	if err := validateGraphApplyPlan(plan, nil); err == nil {
		t.Fatal("validateGraphApplyPlan should reject unknown type \"frobnicate\"")
	}
}

// TestMaterializeGraphNodeIssue_NormalizesType verifies the materialize step
// (proxied create path) stores the CANONICAL type for an alias, matching the
// validation pass and bd create. Empty type still defaults to task.
func TestMaterializeGraphNodeIssue_NormalizesType(t *testing.T) {
	cases := []struct {
		nodeType string
		want     types.IssueType
	}{
		{"feat", types.TypeFeature},
		{"mol", types.TypeMolecule},
		{"investigation", types.TypeSpike},
		{"BUG", types.TypeBug},
		{"", types.TypeTask}, // empty defaults to task
	}
	for _, c := range cases {
		got := materializeGraphNodeIssue(GraphApplyNode{Key: "k", Title: "t", Type: c.nodeType}, createInput{})
		if got.IssueType != c.want {
			t.Errorf("materializeGraphNodeIssue type %q -> %q, want %q", c.nodeType, got.IssueType, c.want)
		}
	}
}
