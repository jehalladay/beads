package main

import (
	"context"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// beads-sq19v: getEpicChildren returns ONLY direct parent-child dependents (one
// level). A child that is itself an epic (or molecule) was placed into the plan
// as a single leaf worker-session, and its own children (grandchildren of the
// root) never entered analysis.Issues — total_issues under-counted, the child
// epic scheduled as if it were executable work, and (because it produced zero
// Errors) analysis.Swarmable stayed true with NO warning. That is false
// confidence: a whole subtree of unplanned work is hidden behind a
// swarmable:true / warnings:[] verdict.
//
// The fix warns when a child's IssueType is epic or molecule, so the swarmable
// verdict is no longer silently wrong about the real actionable-work set
// (sibling invariant to beads-j75r, which fixed the childless-epic direction).
// It preserves the one-level design; transitive grandchild expansion is a
// separate design choice deliberately not taken here.
//
// MUTATION-VERIFY: delete the `IssueType == TypeEpic || TypeMolecule` warning
// block in analyzeEpicForSwarm and this test FAILS — no nested-epic warning is
// emitted for the child epic.
func TestAnalyzeEpicForSwarm_NestedChildEpicWarns_sq19v(t *testing.T) {
	ctx := context.Background()
	epic := &types.Issue{ID: "root", Title: "Root epic", IssueType: types.TypeEpic}

	// Mixed level: one executable task child + one child that is itself an epic.
	// The child epic's own children (grandchildren) are intentionally NOT wired
	// as dependents of root — that is exactly the DB shape the bug hides: the
	// grandchildren are only reachable one level deeper, which getEpicChildren
	// never traverses.
	t.Run("child epic emits a nested-work warning", func(t *testing.T) {
		f := newFakeSwarmStore()
		f.dependents["root"] = []*types.Issue{
			{ID: "direct-task", Title: "Direct task", Status: types.StatusOpen, IssueType: types.TypeTask},
			{ID: "child-epic", Title: "Child epic", Status: types.StatusOpen, IssueType: types.TypeEpic},
		}
		f.depRecords["direct-task"] = []*types.Dependency{{DependsOnID: "root", Type: types.DepParentChild}}
		f.depRecords["child-epic"] = []*types.Dependency{{DependsOnID: "root", Type: types.DepParentChild}}

		a, err := analyzeEpicForSwarm(ctx, f, epic)
		if err != nil {
			t.Fatalf("analyze: %v", err)
		}
		if a.TotalIssues != 2 {
			t.Fatalf("precondition: getEpicChildren returns both direct children (task + child epic), got total=%d", a.TotalIssues)
		}
		if !swarmWarnsContain(a.Warnings, "child-epic") || !swarmWarnsContain(a.Warnings, "not expanded") {
			t.Errorf("REGRESSION (sq19v): a child epic hides unplanned nested work but no warning was emitted — swarmable:%v with warnings=%v [beads-sq19v]",
				a.Swarmable, a.Warnings)
		}
	})

	t.Run("child molecule also warns", func(t *testing.T) {
		f := newFakeSwarmStore()
		f.dependents["root"] = []*types.Issue{
			{ID: "child-mol", Title: "Child molecule", Status: types.StatusOpen, IssueType: types.TypeMolecule},
		}
		f.depRecords["child-mol"] = []*types.Dependency{{DependsOnID: "root", Type: types.DepParentChild}}

		a, err := analyzeEpicForSwarm(ctx, f, epic)
		if err != nil {
			t.Fatalf("analyze: %v", err)
		}
		if !swarmWarnsContain(a.Warnings, "child-mol") || !swarmWarnsContain(a.Warnings, "not expanded") {
			t.Errorf("sq19v: a child molecule should also warn about unexpanded nested work, got warnings=%v", a.Warnings)
		}
	})

	// Negative: a flat epic of plain task leaves must NOT emit the nested warning
	// (guards against over-warning that would defeat the swarmable signal).
	t.Run("flat task children do not warn", func(t *testing.T) {
		f := newFakeSwarmStore()
		f.dependents["root"] = []*types.Issue{
			{ID: "a", Title: "a", Status: types.StatusOpen, IssueType: types.TypeTask},
			{ID: "b", Title: "b", Status: types.StatusOpen, IssueType: types.TypeTask},
		}
		f.depRecords["a"] = []*types.Dependency{{DependsOnID: "root", Type: types.DepParentChild}}
		f.depRecords["b"] = []*types.Dependency{{DependsOnID: "root", Type: types.DepParentChild}}

		a, err := analyzeEpicForSwarm(ctx, f, epic)
		if err != nil {
			t.Fatalf("analyze: %v", err)
		}
		if swarmWarnsContain(a.Warnings, "not expanded") {
			t.Errorf("sq19v: flat task children must NOT trigger the nested-epic warning, got %v", a.Warnings)
		}
		if !a.Swarmable {
			t.Errorf("sq19v: a clean flat epic should stay swarmable, got errors=%v", a.Errors)
		}
	})
}
