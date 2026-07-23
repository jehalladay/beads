package dolt

import (
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestGetMoleculeProgress_DoneCategoryStep is the behavioral teeth for
// beads-bobpm: the storage-layer GetMoleculeProgress (issueops.GetMoleculeProgressInTx)
// counted a step toward Completed ONLY on the literal types.StatusClosed, so a
// step moved to a custom DONE-CATEGORY status was silently NOT counted — leaving
// 'bd mol progress' (completed/percent/rate) and the large-molecule summary
// undercounting relative to the cmd-side getMoleculeProgress (beads-x463g),
// autoclose, and bd ready/count/list which all treat a done-category status as
// terminal. The fix resolves the done-category names in-tx and completes on
// StatusClosed OR a done-category name.
//
// This runs the real embedded store end-to-end: config -> custom_statuses table
// -> the in-tx resolution the fix relies on can only be proven through the
// engine (a pure/marshal test would not exercise the SetConfig->SyncCustomStatuses
// path nor the tx-scoped ResolveCustomStatusesDetailedInTx read).
//
// Shape: root -> step (status="resolved", a CategoryDone custom status).
// Before the fix: Completed=0 (resolved != 'closed'). After: Completed=1.
func TestGetMoleculeProgress_DoneCategoryStep(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()
	ctx, cancel := testContext(t)
	defer cancel()

	// Register a custom done-category status: "resolved" is terminal/done.
	if err := store.SetConfig(ctx, "status.custom", "resolved:done"); err != nil {
		t.Fatalf("SetConfig status.custom: %v", err)
	}

	root := &types.Issue{ID: "mb-root", Title: "Molecule", Status: types.StatusOpen, Priority: 1, IssueType: types.TypeTask}
	step := &types.Issue{ID: "mb-step", Title: "Step 1", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask}
	for _, iss := range []*types.Issue{root, step} {
		if err := store.CreateIssue(ctx, iss, "tester"); err != nil {
			t.Fatalf("create %s: %v", iss.ID, err)
		}
	}
	// step is a parent-child of root (a molecule step).
	if err := store.AddDependency(ctx, &types.Dependency{
		IssueID: "mb-step", DependsOnID: "mb-root", Type: types.DepParentChild,
	}, "tester"); err != nil {
		t.Fatalf("add dep: %v", err)
	}

	// Move the step into the custom done-category status (NOT literal 'closed').
	if err := store.UpdateIssue(ctx, "mb-step", map[string]interface{}{"status": "resolved"}, "tester"); err != nil {
		t.Fatalf("update mb-step to resolved: %v", err)
	}

	stats, err := store.GetMoleculeProgress(ctx, "mb-root")
	if err != nil {
		t.Fatalf("GetMoleculeProgress: %v", err)
	}

	if stats.Total != 1 {
		t.Fatalf("Total = %d, want 1 (one step)", stats.Total)
	}
	// The RED assertion: before the fix a done-category step was not counted
	// (Completed=0), so a fully-resolved molecule reported 0%% here while the
	// cmd-side path + autoclose saw it complete.
	if stats.Completed != 1 {
		t.Fatalf("Completed = %d, want 1 — a custom done-category step must count toward molecule completion (beads-bobpm; storage-layer parity with cmd-side getMoleculeProgress/x463g)", stats.Completed)
	}
	percent := float64(stats.Completed) * 100 / float64(stats.Total)
	if percent < 100 {
		t.Errorf("progress = %.1f%%, want 100%% — the only step is in a done-category status (beads-bobpm)", percent)
	}
}
