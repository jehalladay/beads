package dolt

import (
	"context"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// beads-i9bui: the wisp-source dep-add path (DoltStore.addWispDependency) used
// to cycle-check ONLY dep.Type==blocks (bd-xe27, pre-dating the family sweep),
// while the issue-source seam (issueops.CheckDependencyCycleInTx via
// cycleAuditedFamilies) rejects a cycle for the WHOLE write-family:
// blocks+conditional-blocks, parent-child (beads-8qij), supersedes (beads-8ix02).
// So a wisp-source conditional-blocks / parent-child / supersedes edge could
// close a cycle the issue-source path rejects (and the embedded backend, which
// always routes through the seam, rejected — a backend-asymmetric hole).
//
// These tests build a mutual cycle A->B / B->A of each non-blocks family type
// with a WISP source and assert AddDependency rejects the closing edge. Before
// the fix, addWispDependency's blocks-only gate let all three through.
// waits-for is intentionally NOT tested here — it is OWNER-HELD (beads-vdqra):
// cycleCheckTypesFor deliberately returns nil for waits-for (beads-8qij's
// tested exemption), so routing the wisp path through the shared seam
// preserves that exemption automatically.

// addWispSourceDep adds an edge of the given type with a WISP source, failing
// the test if the add itself errors (used to build the first leg of a cycle).
func addWispSourceDep(t *testing.T, ctx context.Context, store *DoltStore, issueID, dependsOnID string, depType types.DependencyType) {
	t.Helper()
	if err := store.AddDependency(ctx, &types.Dependency{
		IssueID:     issueID,
		DependsOnID: dependsOnID,
		Type:        depType,
	}, "tester"); err != nil {
		t.Fatalf("AddDependency %s->%s (%s): %v", issueID, dependsOnID, depType, err)
	}
}

func TestAddWispDependencyRejectsConditionalBlocksCycle_i9bui(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx, cancel := testContext(t)
	defer cancel()

	const (
		wispA = "cbcyc-wisp-a"
		wispB = "cbcyc-wisp-b"
	)
	createWisp(t, ctx, store, wispA)
	createWisp(t, ctx, store, wispB)

	// A conditional-blocks B, then B conditional-blocks A closes the cycle.
	addWispSourceDep(t, ctx, store, wispA, wispB, types.DepConditionalBlocks)
	err := store.AddDependency(ctx, &types.Dependency{
		IssueID:     wispB,
		DependsOnID: wispA,
		Type:        types.DepConditionalBlocks,
	}, "tester")
	assertCycleError(t, err)
}

func TestAddWispDependencyRejectsParentChildCycle_i9bui(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx, cancel := testContext(t)
	defer cancel()

	const (
		wispA = "pccyc-wisp-a"
		wispB = "pccyc-wisp-b"
	)
	createWisp(t, ctx, store, wispA)
	createWisp(t, ctx, store, wispB)

	// A parent-child B, then B parent-child A closes the cycle.
	addWispSourceDep(t, ctx, store, wispA, wispB, types.DepParentChild)
	err := store.AddDependency(ctx, &types.Dependency{
		IssueID:     wispB,
		DependsOnID: wispA,
		Type:        types.DepParentChild,
	}, "tester")
	assertCycleError(t, err)
}

func TestAddWispDependencyRejectsSupersedesCycle_i9bui(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx, cancel := testContext(t)
	defer cancel()

	const (
		wispA = "supcyc-wisp-a"
		wispB = "supcyc-wisp-b"
	)
	createWisp(t, ctx, store, wispA)
	createWisp(t, ctx, store, wispB)

	// A supersedes B (a legal forward link), then B supersedes A closes the
	// cycle. A legitimate forward chain stays allowed (reachability=0); only the
	// closing back-edge is rejected.
	addWispSourceDep(t, ctx, store, wispA, wispB, types.DepSupersedes)
	err := store.AddDependency(ctx, &types.Dependency{
		IssueID:     wispB,
		DependsOnID: wispA,
		Type:        types.DepSupersedes,
	}, "tester")
	assertCycleError(t, err)
}

// TestAddWispDependencyAllowsLegalSupersedeChain_i9bui guards against
// over-rejection: a forward wisp-source supersede chain v1->v2->v3 is legitimate
// and must stay allowed (the reachability walk permits it — the same guarantee
// the issue-source seam gives, beads-8ix02).
func TestAddWispDependencyAllowsLegalSupersedeChain_i9bui(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx, cancel := testContext(t)
	defer cancel()

	const (
		wispV1 = "supchain-wisp-v1"
		wispV2 = "supchain-wisp-v2"
		wispV3 = "supchain-wisp-v3"
	)
	createWisp(t, ctx, store, wispV1)
	createWisp(t, ctx, store, wispV2)
	createWisp(t, ctx, store, wispV3)

	addWispSourceDep(t, ctx, store, wispV1, wispV2, types.DepSupersedes)
	// v2->v3 extends the chain; must NOT be rejected as a cycle.
	if err := store.AddDependency(ctx, &types.Dependency{
		IssueID:     wispV2,
		DependsOnID: wispV3,
		Type:        types.DepSupersedes,
	}, "tester"); err != nil {
		t.Fatalf("legal forward supersede chain v2->v3 was rejected: %v", err)
	}
}
