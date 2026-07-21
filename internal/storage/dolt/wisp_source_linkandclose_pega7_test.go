//go:build cgo

package dolt

import (
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestWispSourceLinkAndClose_pega7 verifies beads-pega7: bd duplicate / bd
// supersede of a WISP (which reach DoltStore.LinkAndClose with a wisp-source
// dependency) must SUCCEED on the hub-connected sql-server backend and route
// the edge to wisp_dependencies — matching the embedded backend
// (embeddeddolt.LinkAndClose, which auto-detects) and this store's own
// AddDependency.
//
// Before the fix, dolt.LinkAndClose hardcoded opts.SourceTable:"issues", so a
// wisp-source LinkAndClose hit `SELECT issue_type FROM issues WHERE id=<wisp>`
// -> ErrNoRows -> a misleading "issue <id> not found" (the wisp DOES exist).
// The embedded backend succeeded — a backend-asymmetric enforcement hole in
// the same slmql/i9bui/k5oqp/cjvxq un-mirrored-guard family on the wisp dep
// path.
//
// MUTATION-VERIFY: restore `SourceTable: "issues"` (and `WriteTable:
// "dependencies"`) in LinkAndClose and this test FAILS at the LinkAndClose call
// with "issue <wisp> not found".
func TestWispSourceLinkAndClose_pega7(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx, cancel := testContext(t)
	defer cancel()

	const (
		wispDup   = "pega7-wisp-dup"
		wispCanon = "pega7-wisp-canon"
	)
	// Both source and target are active wisps: bd duplicate resolves a partial
	// ID via ResolvePartialID (falls back to wisps), so a wisp is a valid arg
	// for both the duplicate and its canonical.
	createWisp(t, ctx, store, wispDup)
	createWisp(t, ctx, store, wispCanon)

	// Wisp-source LinkAndClose (the bd duplicate/supersede path) must SUCCEED.
	dep := &types.Dependency{
		IssueID:     wispDup,
		DependsOnID: wispCanon,
		Type:        types.DepDuplicates,
	}
	if err := store.LinkAndClose(ctx, dep, "adder"); err != nil {
		t.Fatalf("wisp-source LinkAndClose: expected success (beads-pega7), got %v", err)
	}

	// (2) The edge must land in wisp_dependencies, NOT the permanent
	// dependencies table.
	var wispEdges int
	if err := store.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM wisp_dependencies WHERE issue_id = ? AND depends_on_wisp_id = ?`,
		wispDup, wispCanon).Scan(&wispEdges); err != nil {
		t.Fatalf("count wisp_dependencies: %v", err)
	}
	if wispEdges != 1 {
		t.Errorf("wisp_dependencies edge count = %d, want 1 (edge must route to wisp_dependencies)", wispEdges)
	}

	var permEdges int
	if err := store.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM dependencies WHERE issue_id = ?`, wispDup).Scan(&permEdges); err != nil {
		t.Fatalf("count dependencies: %v", err)
	}
	if permEdges != 0 {
		t.Errorf("permanent dependencies edge count = %d, want 0 (wisp-source edge must NOT land in dependencies)", permEdges)
	}

	// (3) The close half must close the wisp source.
	got, err := store.GetIssue(ctx, wispDup)
	if err != nil {
		t.Fatalf("GetIssue %s after link+close: %v", wispDup, err)
	}
	if got.Status != types.StatusClosed {
		t.Errorf("wisp source status = %q after link+close, want %q", got.Status, types.StatusClosed)
	}
}
