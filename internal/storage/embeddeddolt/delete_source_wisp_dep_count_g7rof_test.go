//go:build cgo

package embeddeddolt_test

import (
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestDeleteIssuesSourceWispDepCount_g7rof pins beads-g7rof: bd purge/prune
// under-reported "Dependencies removed" for the SOURCE side of a directly-purged
// wisp's own outgoing dependency.
//
// issueops.DeleteIssuesInTx counted source-side wisp_dependencies rows over
// cascadeWispIDs only, which OMITS initialWispIDs — wisps named directly in the
// purge (as opposed to cascadeWispIDs pulled in via --cascade). wisp_dependencies
// carries fk_wisp_dep_issue ON DELETE CASCADE (migration 0021), so a directly-
// purged wisp W with an outgoing edge W→P has that row FK-cascade-removed but
// counted 0 → "Dependencies removed: 0" understated. This is the SOURCE-side
// sibling of beads-tvmdn (which fixed the INBOUND-target loop only). The fix
// counts wisp_dependencies over allWispIDsDedup (initialWispIDs ∪ cascadeWispIDs,
// deduped so a doubly-listed wisp is not double-counted).
//
// Embedded backend: it passes wisp IDs straight to DeleteIssuesInTx (the server
// DoltStore pre-partitions wisps out via deleteWispBatch), so this is the backend
// that exercises the source-side count with an initialWispID present.
//
// MUTATION-VERIFIED: reverting the count to `cascadeWispIDs` makes this assert 1
// but get 0.
func TestDeleteIssuesSourceWispDepCount_g7rof(t *testing.T) {
	te := newTestEnv(t, "g7rof")
	ctx := t.Context()

	// Regular issue P — the surviving TARGET of the wisp's outgoing edge.
	p := &types.Issue{
		ID: "g7rof-p", Title: "regular target", Status: types.StatusOpen,
		Priority: 1, IssueType: types.TypeTask,
	}
	if err := te.store.CreateIssue(ctx, p, "tester"); err != nil {
		t.Fatalf("create P: %v", err)
	}

	// Ephemeral wisp W — the directly-purged SOURCE (explicit wisp ID). Its
	// outgoing dep is stored in wisp_dependencies with issue_id = W.
	w := &types.Issue{
		ID: "g7rof-wisp-1", Title: "wisp source", Status: types.StatusOpen,
		Priority: 3, IssueType: types.TypeTask, Ephemeral: true,
	}
	if err := te.store.CreateIssue(ctx, w, "tester"); err != nil {
		t.Fatalf("create W (wisp): %v", err)
	}

	// Outgoing edge W → P (wisp source, regular target → stored in
	// wisp_dependencies, issue_id = W).
	dep := &types.Dependency{IssueID: w.ID, DependsOnID: p.ID, Type: types.DepBlocks}
	if err := te.store.AddDependency(ctx, dep, "tester"); err != nil {
		t.Fatalf("add W->P dep: %v", err)
	}

	// Purge ONLY the wisp (dry-run = count-only). W is a directly named wisp →
	// initialWispIDs, excluded from cascadeWispIDs; W's own W→P source dep row is
	// cut by the FK cascade and MUST be counted exactly once.
	result, err := te.store.DeleteIssues(ctx, []string{w.ID}, false, true, true)
	if err != nil {
		t.Fatalf("delete wisp: %v", err)
	}
	if result.DependenciesCount != 1 {
		t.Errorf("expected 1 source dependency removed (W->P), got %d — source-side wisp_dependencies count missed the directly-purged wisp (beads-g7rof)", result.DependenciesCount)
	}
}
