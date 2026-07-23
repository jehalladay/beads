//go:build cgo

package dolt

import (
	"context"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestWispSourceDepReAddPreservesGateMetadata verifies beads-dh21c: the
// wisp-source (DoltStore.addWispDependency) leg of the beads-xkpb4
// edge-metadata-clobber class. A same-type idempotent re-add that carries NO
// metadata must PRESERVE the existing row's waits-for gate metadata, not silently
// reset it to the all-children default.
//
// addWispDependency coerces an empty dep.Metadata to "{}" and — before the fix —
// did an UNCONDITIONAL `UPDATE wisp_dependencies SET metadata = ?` on the
// same-type branch. So re-adding a `waits-for` edge that was created with
// {"gate":"any-children"} but re-added metadata-less reverted the gate to
// all-children (ParseWaitsForGateMetadata returns all-children on empty/"{}").
// The fix guards the UPDATE on dep.Metadata != "" (the caller's intent).
func TestWispSourceDepReAddPreservesGateMetadata_dh21c(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx, cancel := testContext(t)
	defer cancel()

	const (
		wispA = "dh21c-wisp-a"
		wispB = "dh21c-wisp-b"
	)
	createWisp(t, ctx, store, wispA)
	createWisp(t, ctx, store, wispB)

	// 1. Add a waits-for edge with an explicit any-children gate.
	gated := &types.Dependency{
		IssueID:     wispA,
		DependsOnID: wispB,
		Type:        types.DepWaitsFor,
		Metadata:    `{"gate":"any-children"}`,
	}
	if err := store.AddDependency(ctx, gated, "adder"); err != nil {
		t.Fatalf("AddDependency (initial gated wisp edge): %v", err)
	}
	if got := gateOf(t, store, ctx, wispA, wispB); got != types.WaitsForAnyChildren {
		t.Fatalf("precondition: gate after initial add = %q, want %q", got, types.WaitsForAnyChildren)
	}

	// 2. Re-add the SAME edge with NO metadata (as bulk/idempotent re-adds do).
	//    This is the clobber trigger: metadata-less re-add must not touch the gate.
	reAdd := &types.Dependency{
		IssueID:     wispA,
		DependsOnID: wispB,
		Type:        types.DepWaitsFor,
		// Metadata intentionally empty.
	}
	if err := store.AddDependency(ctx, reAdd, "adder"); err != nil {
		t.Fatalf("AddDependency (metadata-less re-add): %v", err)
	}

	// 3. The any-children gate must survive the re-add.
	if got := gateOf(t, store, ctx, wispA, wispB); got != types.WaitsForAnyChildren {
		t.Errorf("beads-dh21c: gate after metadata-less re-add = %q, want %q (metadata-less re-add clobbered the gate to the all-children default)", got, types.WaitsForAnyChildren)
	}
}

// gateOf reads the waits-for gate of the (from -> to) wisp dependency edge via
// getWispDependencyRecords (the same read path bd export/`bd dep` observe).
func gateOf(t *testing.T, store *DoltStore, ctx context.Context, from, to string) string {
	t.Helper()
	recs, err := store.getWispDependencyRecords(ctx, from)
	if err != nil {
		t.Fatalf("getWispDependencyRecords(%s): %v", from, err)
	}
	for _, d := range recs {
		if d.DependsOnID == to && d.Type == types.DepWaitsFor {
			return types.ParseWaitsForGateMetadata(d.Metadata)
		}
	}
	t.Fatalf("no waits-for edge %s -> %s found in wisp dependency records", from, to)
	return ""
}
