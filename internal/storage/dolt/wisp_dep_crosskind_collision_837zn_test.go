package dolt

import (
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/storage/depid"
	"github.com/steveyegge/beads/internal/types"
)

// TestAddWispDependencySurfacesCrossKindIDCollision is the beads-837zn tooth for
// the wisp-source leg (DoltStore.addWispDependency). depid.New keys a dependency
// edge id on the flattened (issue_id, target-string) with no target-kind marker,
// so a wisp-target edge and an issue-target edge that share the same target id
// derive the SAME wisp_dependencies primary key while uk_wisp_dep_wisp_target and
// uk_wisp_dep_issue_target treat them as distinct edges. Before this fix the
// wisp leg ran a plain INSERT with no rowsAffected probe: an existing issue-kind
// row at that PK made the wisp-kind INSERT silently no-op (its kind-discriminated
// pre-check found nothing, then ON-DUPLICATE-nothing collapsed the edge). This
// asserts the collision is now surfaced as an error, and the pre-existing
// issue-target row survives.
func TestAddWispDependencySurfacesCrossKindIDCollision(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx, cancel := testContext(t)
	defer cancel()

	// Source wisp A (routes AddDependency to the wisp_dependencies path).
	srcWisp := &types.Issue{ID: "837zn-w-src", Title: "src wisp", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask, Ephemeral: true}
	if err := store.CreateIssue(ctx, srcWisp, "tester"); err != nil {
		t.Fatalf("create source wisp: %v", err)
	}

	// Target X must exist as BOTH an issue (FK for the seeded issue-target row)
	// and a wisp (FK + wisp-kind classification for the added edge) — the
	// issue-space/wisp-space id collision that is the whole beads-xaxe premise.
	// CreateIssue enforces cross-table uniqueness (beads-tnv9), so seed both rows
	// with raw INSERTs that bypass that application-level guard; the DB has no
	// cross-table constraint preventing the same id in issues and wisps.
	if _, err := store.db.ExecContext(ctx,
		"INSERT INTO issues (id, title, description, design, acceptance_criteria, notes, status, priority, issue_type) VALUES (?, 'tgt issue', '', '', '', '', 'open', 2, 'task')",
		"837zn-w-tgt"); err != nil {
		t.Fatalf("seed target issue: %v", err)
	}
	if _, err := store.db.ExecContext(ctx,
		"INSERT INTO wisps (id, title) VALUES (?, 'tgt wisp')",
		"837zn-w-tgt"); err != nil {
		t.Fatalf("seed target wisp: %v", err)
	}

	// Pre-seed an ISSUE-target edge A -> X into wisp_dependencies at the
	// deterministic PK depid.New(A, X).
	if _, err := store.db.ExecContext(ctx,
		"INSERT INTO wisp_dependencies (id, issue_id, depends_on_issue_id, type, created_by) VALUES (?, ?, ?, 'blocks', 'tester')",
		depid.New("837zn-w-src", "837zn-w-tgt"), "837zn-w-src", "837zn-w-tgt"); err != nil {
		t.Fatalf("seed colliding issue-target wisp_dependencies row: %v", err)
	}

	// Adding a WISP-target edge A -> X (X is an active wisp) derives the SAME
	// depid.New PK → cross-kind collision. Must be surfaced, not silently no-op'd.
	dep := &types.Dependency{IssueID: "837zn-w-src", DependsOnID: "837zn-w-tgt", Type: types.DepBlocks}
	err := store.AddDependency(ctx, dep, "tester")
	if err == nil {
		t.Fatal("cross-kind PK collision must be surfaced, but AddDependency returned nil (silent collapse)")
	}
	if !strings.Contains(err.Error(), "different target kind") {
		t.Fatalf("err = %v, want a cross-kind collision error mentioning 'different target kind'", err)
	}

	// The pre-existing issue-target row must survive untouched, and no
	// wisp-target row may have been written.
	var issueKindRows, wispKindRows int
	if err := store.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM wisp_dependencies WHERE id = ? AND depends_on_issue_id = ?",
		depid.New("837zn-w-src", "837zn-w-tgt"), "837zn-w-tgt").Scan(&issueKindRows); err != nil {
		t.Fatalf("count issue-kind row: %v", err)
	}
	if issueKindRows != 1 {
		t.Errorf("pre-existing issue-target row = %d, want 1 (must survive the rejected collision)", issueKindRows)
	}
	if err := store.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM wisp_dependencies WHERE issue_id = ? AND depends_on_wisp_id = ?",
		"837zn-w-src", "837zn-w-tgt").Scan(&wispKindRows); err != nil {
		t.Fatalf("count wisp-kind row: %v", err)
	}
	if wispKindRows != 0 {
		t.Errorf("wisp-target rows = %d, want 0 (the colliding edge must not have been written)", wispKindRows)
	}
}
