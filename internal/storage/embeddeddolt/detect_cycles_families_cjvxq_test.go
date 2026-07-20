//go:build cgo

package embeddeddolt_test

import (
	"context"
	"testing"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/storage/embeddeddolt"
	"github.com/steveyegge/beads/internal/types"
)

// beads-cjvxq: DetectCycles audited ONLY blocks/conditional-blocks, but the
// create-time invariant (cycleCheckTypesFor) also protects parent-child
// (beads-8qij) and supersedes (beads-8ix02). A cycle in either family — which
// the per-edge write guard cannot itself create, but a Dolt branch/clone MERGE
// of two individually-acyclic halves CAN produce — was invisible to
// `bd dep cycles`, giving a false "no cycles" all-clear on exactly the families
// the audit exists to verify. These tests seed a cycle via SkipCycleCheck
// (which is precisely the merge/bulk path that bypasses the per-edge guard) and
// assert DetectCycles surfaces it. RED before the family-widening fix, GREEN
// after.

// addCycleEdgesSkippingGuard commits the given edges with SkipCycleCheck, which
// is how a merge or bulk import lands edges without the per-edge cycle guard.
func addCycleEdgesSkippingGuard(t *testing.T, store *embeddeddolt.EmbeddedDoltStore, ctx context.Context, edges []*types.Dependency) {
	t.Helper()
	err := store.RunInTransaction(ctx, "seed cycle (skip guard)", func(tx storage.Transaction) error {
		for _, dep := range edges {
			if err := tx.AddDependencyWithOptions(ctx, dep, "tester", storage.DependencyAddOptions{SkipCycleCheck: true}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seed cycle via SkipCycleCheck: %v", err)
	}
}

func TestDetectCycles_ParentChildCycle_cjvxq(t *testing.T) {
	skipUnlessEmbeddedDolt(t)
	te := newTestEnv(t, "pc")
	ctx := t.Context()

	for _, id := range []string{"pc-a", "pc-b", "pc-c"} {
		iss := &types.Issue{ID: id, Title: id, Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask}
		if err := te.store.CreateIssue(ctx, iss, "tester"); err != nil {
			t.Fatalf("CreateIssue %s: %v", id, err)
		}
	}
	if err := te.store.Commit(ctx, "issues"); err != nil {
		t.Fatalf("Commit issues: %v", err)
	}

	// parent-child cycle a -> b -> c -> a (bypassing the per-edge guard, as a merge would).
	addCycleEdgesSkippingGuard(t, te.store, ctx, []*types.Dependency{
		{IssueID: "pc-a", DependsOnID: "pc-b", Type: types.DepParentChild},
		{IssueID: "pc-b", DependsOnID: "pc-c", Type: types.DepParentChild},
		{IssueID: "pc-c", DependsOnID: "pc-a", Type: types.DepParentChild},
	})

	cycles, err := te.store.DetectCycles(ctx)
	if err != nil {
		t.Fatalf("DetectCycles: %v", err)
	}
	if len(cycles) == 0 {
		t.Fatal("DetectCycles missed a parent-child cycle (beads-cjvxq): reported a false 'no cycles' all-clear")
	}
}

func TestDetectCycles_SupersedesCycle_cjvxq(t *testing.T) {
	skipUnlessEmbeddedDolt(t)
	te := newTestEnv(t, "sc")
	ctx := t.Context()

	for _, id := range []string{"sc-a", "sc-b"} {
		iss := &types.Issue{ID: id, Title: id, Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask}
		if err := te.store.CreateIssue(ctx, iss, "tester"); err != nil {
			t.Fatalf("CreateIssue %s: %v", id, err)
		}
	}
	if err := te.store.Commit(ctx, "issues"); err != nil {
		t.Fatalf("Commit issues: %v", err)
	}

	// supersedes cycle a -> b -> a (a legitimate forward chain a->b would NOT
	// cycle; this closes it, as a cross-branch merge could).
	addCycleEdgesSkippingGuard(t, te.store, ctx, []*types.Dependency{
		{IssueID: "sc-a", DependsOnID: "sc-b", Type: types.DepSupersedes},
		{IssueID: "sc-b", DependsOnID: "sc-a", Type: types.DepSupersedes},
	})

	cycles, err := te.store.DetectCycles(ctx)
	if err != nil {
		t.Fatalf("DetectCycles: %v", err)
	}
	if len(cycles) == 0 {
		t.Fatal("DetectCycles missed a supersedes cycle (beads-cjvxq): reported a false 'no cycles' all-clear")
	}
}

// A cross-family path (blocks -> parent-child -> blocks) is NOT a real cycle in
// either family and must NOT be reported — the audit checks each family against
// its own graph, matching the write guard's per-family semantics.
func TestDetectCycles_CrossFamilyPathIsNotACycle_cjvxq(t *testing.T) {
	skipUnlessEmbeddedDolt(t)
	te := newTestEnv(t, "xf")
	ctx := t.Context()

	for _, id := range []string{"xf-a", "xf-b", "xf-c"} {
		iss := &types.Issue{ID: id, Title: id, Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask}
		if err := te.store.CreateIssue(ctx, iss, "tester"); err != nil {
			t.Fatalf("CreateIssue %s: %v", id, err)
		}
	}
	if err := te.store.Commit(ctx, "issues"); err != nil {
		t.Fatalf("Commit issues: %v", err)
	}

	// a -blocks-> b -parent-child-> c -blocks-> a : no single family forms a loop.
	addCycleEdgesSkippingGuard(t, te.store, ctx, []*types.Dependency{
		{IssueID: "xf-a", DependsOnID: "xf-b", Type: types.DepBlocks},
		{IssueID: "xf-b", DependsOnID: "xf-c", Type: types.DepParentChild},
		{IssueID: "xf-c", DependsOnID: "xf-a", Type: types.DepBlocks},
	})

	cycles, err := te.store.DetectCycles(ctx)
	if err != nil {
		t.Fatalf("DetectCycles: %v", err)
	}
	if len(cycles) != 0 {
		t.Fatalf("DetectCycles wrongly flagged a cross-family path as a cycle (beads-cjvxq): %v", cycles)
	}
}
