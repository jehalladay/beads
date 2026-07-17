package dolt

import (
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestChildMintSkipsCrossTableCollision pins beads-tnv9 tooth (1): auto-minting
// a child of an issue-parent must NOT return an id that already exists as a
// wisp (and vice-versa). issues and wisps are separate tables with no
// cross-table uniqueness, so GetNextChildIDTx — which scanned only the parent's
// OWN table for existing children — could mint parent.N while parent.N already
// lived in the OTHER table (the orphaned-wisp-child precondition left by
// promote, or a bulk/explicit-id import). Same xaxe/uekw/jym1/mgsx family.
func TestChildMintSkipsCrossTableCollision(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx, cancel := testContext(t)
	defer cancel()

	// An issue-parent P (permanent, issues table).
	parent := &types.Issue{ID: "tnv9-p", Title: "parent", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask}
	if err := store.CreateIssue(ctx, parent, "tester"); err != nil {
		t.Fatalf("create issue parent: %v", err)
	}
	// An orphaned wisp child P.1 living in the WISPS table (the exact residue a
	// wisp-promote leaves: promote deletes wisp P but not its children).
	orphan := &types.Issue{ID: "tnv9-p.1", Title: "orphan child", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask, Ephemeral: true}
	if err := store.CreateIssue(ctx, orphan, "tester"); err != nil {
		t.Fatalf("create orphan wisp child: %v", err)
	}
	// Sanity: the orphan is in wisps, and the issues side has no P.1.
	assertRowCountForIssue(t, store, "wisps", "tnv9-p.1", 1)
	assertRowCountForIssue(t, store, "issues", "tnv9-p.1", 0)

	// Mint the next child of the issue-parent P. The issues-side counter is 0
	// and there are no issues-side children, so pre-fix this returns "tnv9-p.1"
	// — colliding with the orphaned wisp. The fix must skip past it.
	got, err := store.GetNextChildID(ctx, "tnv9-p")
	if err != nil {
		t.Fatalf("GetNextChildID: %v", err)
	}
	if got == "tnv9-p.1" {
		t.Fatalf("GetNextChildID minted %q which already exists as a wisp — cross-table collision", got)
	}
	if got != "tnv9-p.2" {
		t.Fatalf("GetNextChildID = %q, want tnv9-p.2 (bumped past the cross-table child)", got)
	}
}

// TestInsertIssueRejectsCrossTableIDCollision pins beads-tnv9 tooth (2): the
// shared create chokepoint InsertIssueIfNew must reject inserting an issue whose
// id already exists as a wisp (or vice-versa), fail-closed with a clear error —
// mirroring the promote (jym1) and rename (mgsx) guards. This covers the direct,
// proxied, bulk-import and explicit-id create stacks, which all funnel through
// InsertIssueIfNew.
func TestInsertIssueRejectsCrossTableIDCollision(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx, cancel := testContext(t)
	defer cancel()

	// Seed a permanent issue X.
	iss := &types.Issue{ID: "tnv9-x", Title: "issue", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask}
	if err := store.CreateIssue(ctx, iss, "tester"); err != nil {
		t.Fatalf("create issue: %v", err)
	}

	// Attempt to create a WISP carrying the same id X → must be rejected before
	// it mints a same-id issue+wisp.
	wisp := &types.Issue{ID: "tnv9-x", Title: "wisp collide", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask, Ephemeral: true}
	err := store.CreateIssue(ctx, wisp, "tester")
	if err == nil || !strings.Contains(err.Error(), "already exists as an issue") {
		t.Fatalf("wisp create over existing issue id: err = %v, want 'already exists as an issue'", err)
	}
	// The wisps table must NOT have gained the colliding id.
	assertRowCountForIssue(t, store, "wisps", "tnv9-x", 0)
	assertRowCountForIssue(t, store, "issues", "tnv9-x", 1)

	// Symmetric direction: seed a wisp W, attempt to create a permanent issue W.
	w := &types.Issue{ID: "tnv9-w", Title: "wisp", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask, Ephemeral: true}
	if err := store.CreateIssue(ctx, w, "tester"); err != nil {
		t.Fatalf("create wisp: %v", err)
	}
	collideIssue := &types.Issue{ID: "tnv9-w", Title: "issue collide", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask}
	err = store.CreateIssue(ctx, collideIssue, "tester")
	if err == nil || !strings.Contains(err.Error(), "already exists as a wisp") {
		t.Fatalf("issue create over existing wisp id: err = %v, want 'already exists as a wisp'", err)
	}
	assertRowCountForIssue(t, store, "issues", "tnv9-w", 0)
	assertRowCountForIssue(t, store, "wisps", "tnv9-w", 1)
}

// TestPromoteStillSucceedsWithCollisionGuard guards against over-rejection: the
// promote path calls InsertIssueIfNew(issues) while the source wisp row still
// exists (the wisp is deleted afterwards), so a naive cross-table guard would
// break legitimate promotion. Promote must still succeed and leave exactly one
// issue and zero wisps for the id.
func TestPromoteStillSucceedsWithCollisionGuard(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx, cancel := testContext(t)
	defer cancel()

	wisp := &types.Issue{ID: "tnv9-prom", Title: "to promote", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask, Ephemeral: true}
	if err := store.CreateIssue(ctx, wisp, "tester"); err != nil {
		t.Fatalf("create wisp: %v", err)
	}
	if err := store.PromoteFromEphemeral(ctx, "tnv9-prom", "tester"); err != nil {
		t.Fatalf("PromoteFromEphemeral should succeed despite the cross-table guard: %v", err)
	}
	assertRowCountForIssue(t, store, "issues", "tnv9-prom", 1)
	assertRowCountForIssue(t, store, "wisps", "tnv9-prom", 0)
}
