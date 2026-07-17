package dolt

import (
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestDeleteIssue_OrphanWispDependencyEdge probes whether deleting an issue that
// is the TARGET of a wisp_dependencies edge (a wisp blocks-on the issue) cleans
// up the edge row, or leaves a dangling edge pointing at a now-deleted issue
// (orphan-corruption angle for the close/delete/cascade audit).
//
// The dependencies table has an ON DELETE CASCADE FK on depends_on_issue_id ->
// issues(id); wisp_dependencies has fk_wisp_dep_issue_target (depends_on_issue_id)
// -> issues(id) ON DELETE CASCADE too. So a direct DELETE FROM issues SHOULD
// cascade-remove both edge kinds — IF Dolt enforces the FK for a dolt-ignored
// child table (wisp_dependencies). This test asserts the post-delete edge count
// is zero; a nonzero count is an orphaned-edge corruption.
func TestDeleteIssue_OrphanWispDependencyEdge(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx, cancel := testContext(t)
	defer cancel()

	// wisp W blocks-on permanent issue X. The edge lives in wisp_dependencies
	// with depends_on_issue_id = X.
	createPerm(t, ctx, store, "orphan-edge-target")
	createWisp(t, ctx, store, "orphan-edge-wisp-dep")
	if err := store.AddDependency(ctx, &types.Dependency{
		IssueID:     "orphan-edge-wisp-dep",
		DependsOnID: "orphan-edge-target",
		Type:        types.DepBlocks,
	}, "tester"); err != nil {
		t.Fatalf("add wisp->issue dependency: %v", err)
	}

	// Precondition: the edge exists.
	var before int
	if err := store.DB().QueryRowContext(ctx,
		"SELECT COUNT(*) FROM wisp_dependencies WHERE depends_on_issue_id = ?",
		"orphan-edge-target").Scan(&before); err != nil {
		t.Fatalf("count wisp_dependencies before: %v", err)
	}
	if before != 1 {
		t.Fatalf("precondition: wisp_dependencies edge count = %d, want 1", before)
	}

	// Delete the TARGET issue with --force (orphan mode): the depender wisp is
	// intentionally NOT cascade-deleted, so we isolate whether the EDGE row is
	// cleaned when its target issue disappears.
	if _, err := store.DeleteIssues(ctx, []string{"orphan-edge-target"}, false, true, false); err != nil {
		t.Fatalf("DeleteIssues force: %v", err)
	}

	var after int
	if err := store.DB().QueryRowContext(ctx,
		"SELECT COUNT(*) FROM wisp_dependencies WHERE depends_on_issue_id = ?",
		"orphan-edge-target").Scan(&after); err != nil {
		t.Fatalf("count wisp_dependencies after: %v", err)
	}
	if after != 0 {
		t.Fatalf("ORPHAN EDGE: wisp_dependencies still has %d row(s) pointing at deleted issue 'orphan-edge-target' — dangling edge not cleaned on delete", after)
	}
}

// TestDeleteIssue_IssueDependsOnWisp_EdgeCleaned probes the NO-FK direction:
// an issue that depends on a wisp stores the edge in `dependencies` with
// depends_on_wisp_id set, and there is NO foreign key from
// dependencies.depends_on_wisp_id -> wisps(id) (you can't FK a committed table
// to a dolt-ignored one). So deleting the wisp relies on the manual
// DeleteWispFromDependenciesInTx cleanup, not a cascade. Assert the edge is
// gone after the wisp is deleted; a survivor is a dangling edge.
func TestDeleteIssue_IssueDependsOnWisp_EdgeCleaned(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx, cancel := testContext(t)
	defer cancel()

	// permanent issue P blocks-on wisp W: edge in `dependencies`, depends_on_wisp_id = W.
	createPerm(t, ctx, store, "iodw-issue")
	createWisp(t, ctx, store, "iodw-wisp-target")
	if err := store.AddDependency(ctx, &types.Dependency{
		IssueID:     "iodw-issue",
		DependsOnID: "iodw-wisp-target",
		Type:        types.DepBlocks,
	}, "tester"); err != nil {
		t.Fatalf("add issue->wisp dependency: %v", err)
	}

	var before int
	if err := store.DB().QueryRowContext(ctx,
		"SELECT COUNT(*) FROM dependencies WHERE depends_on_wisp_id = ?",
		"iodw-wisp-target").Scan(&before); err != nil {
		t.Fatalf("count dependencies before: %v", err)
	}
	if before != 1 {
		t.Fatalf("precondition: dependencies edge count = %d, want 1", before)
	}

	// Delete the wisp target.
	if err := store.DeleteIssue(ctx, "iodw-wisp-target"); err != nil {
		t.Fatalf("DeleteIssue wisp: %v", err)
	}

	var after int
	if err := store.DB().QueryRowContext(ctx,
		"SELECT COUNT(*) FROM dependencies WHERE depends_on_wisp_id = ?",
		"iodw-wisp-target").Scan(&after); err != nil {
		t.Fatalf("count dependencies after: %v", err)
	}
	if after != 0 {
		t.Fatalf("ORPHAN EDGE: dependencies still has %d row(s) with depends_on_wisp_id pointing at deleted wisp — DeleteWispFromDependenciesInTx did not clean it", after)
	}
}

// TestDeleteIssue_RecreateSameID_NoAuxBleed probes id-reuse bleed: delete an
// issue that has aux rows (label, comment, dependency), then recreate a fresh
// issue with the SAME id, and verify none of the old aux rows survive to bleed
// into the new issue (cf. the promote id-collision class, beads-jym1).
func TestDeleteIssue_RecreateSameID_NoAuxBleed(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx, cancel := testContext(t)
	defer cancel()

	createPerm(t, ctx, store, "reuse-x")
	createPerm(t, ctx, store, "reuse-blocker")
	if err := store.AddDependency(ctx, &types.Dependency{
		IssueID:     "reuse-x",
		DependsOnID: "reuse-blocker",
		Type:        types.DepBlocks,
	}, "tester"); err != nil {
		t.Fatalf("add dependency: %v", err)
	}
	if err := store.AddLabel(ctx, "reuse-x", "stale-label", "tester"); err != nil {
		t.Fatalf("add label: %v", err)
	}

	// Delete X.
	if err := store.DeleteIssue(ctx, "reuse-x"); err != nil {
		t.Fatalf("DeleteIssue: %v", err)
	}

	// Aux rows for X must be gone immediately after delete.
	for _, probe := range []struct {
		table string
		query string
	}{
		{"dependencies", "SELECT COUNT(*) FROM dependencies WHERE issue_id = ?"},
		{"labels", "SELECT COUNT(*) FROM labels WHERE issue_id = ?"},
	} {
		var n int
		if err := store.DB().QueryRowContext(ctx, probe.query, "reuse-x").Scan(&n); err != nil {
			t.Fatalf("count %s after delete: %v", probe.table, err)
		}
		if n != 0 {
			t.Fatalf("AUX BLEED: %s has %d stale row(s) for deleted issue 'reuse-x'", probe.table, n)
		}
	}

	// Recreate a fresh X and confirm it carries none of the old aux data.
	createPerm(t, ctx, store, "reuse-x")
	labels, err := store.GetLabels(ctx, "reuse-x")
	if err != nil {
		t.Fatalf("GetLabels after recreate: %v", err)
	}
	for _, l := range labels {
		if l == "stale-label" {
			t.Fatalf("AUX BLEED: recreated 'reuse-x' inherited stale label %q from the deleted issue", l)
		}
	}
}
