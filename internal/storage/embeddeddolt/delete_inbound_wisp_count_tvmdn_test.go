//go:build cgo

package embeddeddolt_test

import (
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestDeleteIssuesInboundWispDepCount_tvmdn pins beads-tvmdn: bd purge/prune
// under-reported "Dependencies removed" when a surviving regular issue had an
// inbound dependency edge pointing AT a directly-purged wisp.
//
// issueops.DeleteIssuesInTx's inbound-dep count loop batched over
// expandedRegularIDs only (= finalRegularIDs ∪ cascadeWispIDs), which OMITS
// initialWispIDs — wisps named directly in the delete set (as opposed to
// cascadeWispIDs pulled in via --cascade). So an edge P(regular)→W(directly-
// purged wisp) was FK-cascade-removed but counted 0 → "Dependencies removed: 0"
// while one edge was actually cut. depsCount already handled the SOURCE side
// (wisp_dependencies), so only the INBOUND-target loop missed wisp targets. The
// fix iterates the loop over allDeletedSet (every deleted ID: finalRegularIDs ∪
// allWispIDs), so a directly-purged wisp target is counted — one fix covers the
// dry-run and force paths (both read result.DependenciesCount).
//
// The embedded backend passes wisp IDs straight through to DeleteIssuesInTx
// (the server DoltStore pre-partitions wisps out via deleteWispBatch), so this
// is the backend that exercises the inbound loop with an initialWispID present.
//
// MUTATION-VERIFIED: reverting the loop to `range over expandedRegularIDs`
// makes this assert 1 but get 0.
func TestDeleteIssuesInboundWispDepCount_tvmdn(t *testing.T) {
	te := newTestEnv(t, "tvmdn")
	ctx := t.Context()

	// Regular issue P — the SURVIVING source of the inbound edge.
	p := &types.Issue{
		ID: "tvmdn-p", Title: "regular source", Status: types.StatusOpen,
		Priority: 1, IssueType: types.TypeTask,
	}
	if err := te.store.CreateIssue(ctx, p, "tester"); err != nil {
		t.Fatalf("create P: %v", err)
	}

	// Ephemeral wisp W — the directly-purged TARGET (explicit wisp ID).
	w := &types.Issue{
		ID: "tvmdn-wisp-1", Title: "wisp target", Status: types.StatusOpen,
		Priority: 3, IssueType: types.TypeTask, Ephemeral: true,
	}
	if err := te.store.CreateIssue(ctx, w, "tester"); err != nil {
		t.Fatalf("create W (wisp): %v", err)
	}

	// Inbound edge P → W (regular source, wisp target → stored in dependencies
	// with depends_on_wisp_id).
	dep := &types.Dependency{IssueID: p.ID, DependsOnID: w.ID, Type: types.DepBlocks}
	if err := te.store.AddDependency(ctx, dep, "tester"); err != nil {
		t.Fatalf("add P->W dep: %v", err)
	}

	// Purge ONLY the wisp (dry-run = count-only). W is a directly named wisp →
	// initialWispIDs, excluded from expandedRegularIDs; the P→W edge is cut by
	// the FK cascade and MUST be counted exactly once.
	result, err := te.store.DeleteIssues(ctx, []string{w.ID}, false, true, true)
	if err != nil {
		t.Fatalf("delete wisp: %v", err)
	}
	if result.DependenciesCount != 1 {
		t.Errorf("expected 1 inbound dependency removed (P->W), got %d — inbound-dep count loop missed the directly-purged wisp target (beads-tvmdn)", result.DependenciesCount)
	}
}
