package dolt

import (
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestGetMoleculeProgress_RecursiveDescendants is the behavioral teeth for
// beads-1s2q8: GetMoleculeProgress counted only DIRECT children (a one-hop
// parent-child query), so a molecule whose direct children were all closed
// reported 100% even while nested grandchildren remained open. That diverged
// from the RECURSIVE accounting mol current / mol show use
// (loadTemplateSubgraph -> loadDescendants) and from autoclose's descendant
// walk — a false 100%. The fix reuses the same recursive descendant traversal
// (GetDescendantIDsInTx) so progress counts the whole subgraph.
//
// This runs the real embedded store end-to-end: a pure/marshal test could not
// prove the recursive CTE actually walks past the first level through the
// engine. Shape: root -> child (closed) -> grandchild (open). Before the fix
// Total=1/Completed=1 => 100%; after, Total=2/Completed=1 => 50%.
func TestGetMoleculeProgress_RecursiveDescendants(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()
	ctx, cancel := testContext(t)
	defer cancel()

	// root -> child -> grandchild (a nested molecule two levels deep).
	root := &types.Issue{ID: "mr-root", Title: "Molecule", Status: types.StatusOpen, Priority: 1, IssueType: types.TypeTask}
	child := &types.Issue{ID: "mr-child", Title: "Step 1", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask}
	grandchild := &types.Issue{ID: "mr-gc", Title: "Step 1.1", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask}
	for _, iss := range []*types.Issue{root, child, grandchild} {
		if err := store.CreateIssue(ctx, iss, "tester"); err != nil {
			t.Fatalf("create %s: %v", iss.ID, err)
		}
	}
	// child is a parent-child of root; grandchild is a parent-child of child.
	if err := store.AddDependency(ctx, &types.Dependency{
		IssueID: "mr-child", DependsOnID: "mr-root", Type: types.DepParentChild,
	}, "tester"); err != nil {
		t.Fatalf("AddDependency child->root: %v", err)
	}
	if err := store.AddDependency(ctx, &types.Dependency{
		IssueID: "mr-gc", DependsOnID: "mr-child", Type: types.DepParentChild,
	}, "tester"); err != nil {
		t.Fatalf("AddDependency grandchild->child: %v", err)
	}

	// Close ONLY the direct child; the grandchild stays open. Under the old
	// direct-children-only count this reads as 1/1 = 100% (bug); the molecule
	// is NOT actually done because the grandchild is still open.
	if err := store.CloseIssue(ctx, "mr-child", "done", "tester", ""); err != nil {
		t.Fatalf("close mr-child: %v", err)
	}

	stats, err := store.GetMoleculeProgress(ctx, "mr-root")
	if err != nil {
		t.Fatalf("GetMoleculeProgress: %v", err)
	}

	// The grandchild must be counted → Total spans the whole subgraph (2 steps),
	// not just the 1 direct child. This is the RED assertion: before the fix
	// Total=1 (only mr-child).
	if stats.Total != 2 {
		t.Fatalf("Total = %d, want 2 (child + grandchild) — direct-children-only count misses nested descendants (beads-1s2q8)", stats.Total)
	}
	if stats.Completed != 1 {
		t.Fatalf("Completed = %d, want 1 (only mr-child closed)", stats.Completed)
	}
	// The core symptom: progress must NOT be 100% while a nested step is open.
	percent := float64(stats.Completed) * 100 / float64(stats.Total)
	if percent >= 100 {
		t.Errorf("progress = %.1f%%, want < 100%% — a nested open grandchild must keep the molecule below complete (false 100%% bug)", percent)
	}
}
