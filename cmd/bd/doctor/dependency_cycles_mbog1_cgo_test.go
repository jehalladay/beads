//go:build cgo

package doctor

import (
	"context"
	"testing"

	"github.com/steveyegge/beads/internal/storage/dolt"
	"github.com/steveyegge/beads/internal/types"
)

// beads-mbog1: bd doctor's "Dependency Cycles" check was a universal
// false-negative for multi-node cycles. Its hand-rolled recursive CTE guarded
// the recursion with `path NOT LIKE '%<depends_on>→%'`, which excludes the
// cycle-CLOSING edge (the edge whose depends_on == start_id always matches the
// path prefix), so the terminal predicate `depends_on_id = start_id` could only
// ever fire on a depth-0 SELF loop. Every 2+-node cycle was invisible — a false
// "No circular dependencies detected" all-clear on exactly the merge-introduced
// cycles it exists to catch (the same blind spot beads-cjvxq closed in
// DetectCyclesInTx). A SECOND, masked defect: the CTE had no type filter, so any
// fix that kept the untyped graph would turn every bidirectional relates-to link
// into a false-POSITIVE cycle. The fix delegates to the family-aware
// store.DetectCycles, eliminating this third detector.
//
// These tests seed edges via direct INSERT — precisely the merge/bulk path that
// bypasses the per-edge create-time cycle guard — and assert the check now
// agrees with the canonical detector.

// insertDepEdge writes a raw dependency edge, bypassing the per-edge cycle
// guard, as a Dolt branch/clone merge or bulk import would.
func insertDepEdge(t *testing.T, store *dolt.DoltStore, issueID, dependsOn string, depType types.DependencyType) {
	t.Helper()
	db := store.UnderlyingDB()
	// id is any unique value; the checker keys on (issue_id, depends_on, type).
	id := issueID + "->" + dependsOn + ":" + string(depType)
	if _, err := db.Exec(`
		INSERT INTO dependencies (id, issue_id, depends_on_issue_id, type, created_at, created_by)
		VALUES (?, ?, ?, ?, NOW(), 'test')`, id, issueID, dependsOn, string(depType)); err != nil {
		t.Fatalf("insert dep edge %s->%s (%s): %v", issueID, dependsOn, depType, err)
	}
}

func TestDoctorCycles_ThreeNodeBlocksCycle_mbog1(t *testing.T) {
	store := newTestDoltStore(t, "mbog")
	ctx := context.Background()

	for _, id := range []string{"mbog-a", "mbog-b", "mbog-c"} {
		insertIssueDirectly(t, store, id)
	}
	// a -> b -> c -> a, a genuine 3-node blocks cycle (the merge path).
	insertDepEdge(t, store, "mbog-a", "mbog-b", types.DepBlocks)
	insertDepEdge(t, store, "mbog-b", "mbog-c", types.DepBlocks)
	insertDepEdge(t, store, "mbog-c", "mbog-a", types.DepBlocks)

	// Ground truth: the canonical detector sees the cycle.
	cycles, err := store.DetectCycles(ctx)
	if err != nil {
		t.Fatalf("DetectCycles: %v", err)
	}
	if len(cycles) == 0 {
		t.Fatalf("precondition: DetectCycles (ground truth) missed the seeded 3-node cycle")
	}

	check := checkDependencyCyclesWithStore(store)
	if check.Status != StatusError {
		t.Fatalf("doctor Dependency Cycles Status = %q, want %q — false-negative on a 3-node cycle (beads-mbog1): %s",
			check.Status, StatusError, check.Message)
	}
}

func TestDoctorCycles_TwoNodeBlocksCycle_mbog1(t *testing.T) {
	store := newTestDoltStore(t, "mbog")

	for _, id := range []string{"mbog-x", "mbog-y"} {
		insertIssueDirectly(t, store, id)
	}
	// x <-> y blocks cycle.
	insertDepEdge(t, store, "mbog-x", "mbog-y", types.DepBlocks)
	insertDepEdge(t, store, "mbog-y", "mbog-x", types.DepBlocks)

	check := checkDependencyCyclesWithStore(store)
	if check.Status != StatusError {
		t.Fatalf("doctor Dependency Cycles Status = %q, want %q — false-negative on a 2-node cycle (beads-mbog1): %s",
			check.Status, StatusError, check.Message)
	}
}

func TestDoctorCycles_ParentChildCycle_mbog1(t *testing.T) {
	store := newTestDoltStore(t, "mbog")

	for _, id := range []string{"mbog-p", "mbog-q", "mbog-r"} {
		insertIssueDirectly(t, store, id)
	}
	// parent-child cycle — a family the create-time invariant protects but the
	// old untyped CTE could not have distinguished (and the closing-edge bug hid).
	insertDepEdge(t, store, "mbog-p", "mbog-q", types.DepParentChild)
	insertDepEdge(t, store, "mbog-q", "mbog-r", types.DepParentChild)
	insertDepEdge(t, store, "mbog-r", "mbog-p", types.DepParentChild)

	check := checkDependencyCyclesWithStore(store)
	if check.Status != StatusError {
		t.Fatalf("doctor Dependency Cycles Status = %q, want %q — false-negative on a parent-child cycle (beads-mbog1): %s",
			check.Status, StatusError, check.Message)
	}
}

func TestDoctorCycles_NoCycle_mbog1(t *testing.T) {
	store := newTestDoltStore(t, "mbog")

	for _, id := range []string{"mbog-1", "mbog-2", "mbog-3"} {
		insertIssueDirectly(t, store, id)
	}
	// Acyclic chain 1 -> 2 -> 3.
	insertDepEdge(t, store, "mbog-1", "mbog-2", types.DepBlocks)
	insertDepEdge(t, store, "mbog-2", "mbog-3", types.DepBlocks)

	check := checkDependencyCyclesWithStore(store)
	if check.Status != StatusOK {
		t.Fatalf("doctor Dependency Cycles Status = %q, want %q on an acyclic graph: %s",
			check.Status, StatusOK, check.Message)
	}
}

// A bidirectional relates-to link (cmd/bd/relate.go writes BOTH A->B and B->A as
// relates-to edges) is NOT a dependency cycle — relates-to is a loose knowledge
// edge, not in any cycle-audited family. The old untyped CTE would have flagged
// it as a false-POSITIVE cycle if its closing-edge bug were naively fixed; the
// family-aware detector correctly ignores it.
func TestDoctorCycles_BidirectionalRelatesToIsNotACycle_mbog1(t *testing.T) {
	store := newTestDoltStore(t, "mbog")

	for _, id := range []string{"mbog-m", "mbog-n"} {
		insertIssueDirectly(t, store, id)
	}
	insertDepEdge(t, store, "mbog-m", "mbog-n", types.DepRelatesTo)
	insertDepEdge(t, store, "mbog-n", "mbog-m", types.DepRelatesTo)

	check := checkDependencyCyclesWithStore(store)
	if check.Status != StatusOK {
		t.Fatalf("doctor Dependency Cycles Status = %q, want %q — bidirectional relates-to must NOT be a cycle (beads-mbog1): %s",
			check.Status, StatusOK, check.Message)
	}
}
