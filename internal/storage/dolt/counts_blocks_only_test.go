package dolt

import (
	"context"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/types"
)

// TestCountsExcludeParentChild is the regression guard for beads-1d3i: the
// Count* methods that feed the `bd show --json` scalars
// (dependency_count/dependent_count) must count ONLY blocks-type edges, matching
// the list/ready path (GetDependencyCountsInTx filters `type = 'blocks'`).
//
// Before the fix, CountDependencies/CountDependents queried every edge with no
// type filter, so a parent-child hierarchy link inflated the scalar: `bd show
// Z --json` reported dependency_count:1 for a child with only a parent-child
// link while `bd list`/`bd ready` reported 0 for the same issue — one field
// with two meanings. A parent-child link is not a dependency in the
// blocker/readiness sense.
func TestCountsExcludeParentChild(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// cb-epic --parent--> cb-child   (parent-child link; NOT a blocker)
	// cb-blocker --blocks--> cb-child (a real blocker of cb-child)
	// cb-child   --blocks--> cb-goal  (cb-child blocks cb-goal)
	createPerm(t, ctx, store, "cb-epic")
	createPerm(t, ctx, store, "cb-child")
	createPerm(t, ctx, store, "cb-blocker")
	createPerm(t, ctx, store, "cb-goal")

	add := func(src, tgt string, typ types.DependencyType) {
		t.Helper()
		if err := store.AddDependency(ctx, &types.Dependency{
			IssueID:     src,
			DependsOnID: tgt,
			Type:        typ,
		}, "tester"); err != nil {
			t.Fatalf("add dependency %s -> %s (%s): %v", src, tgt, typ, err)
		}
	}
	add("cb-child", "cb-epic", types.DepParentChild)
	add("cb-child", "cb-blocker", types.DepBlocks)
	add("cb-goal", "cb-child", types.DepBlocks)

	// cb-child depends on cb-epic (parent-child) + cb-blocker (blocks).
	// dependency_count must count the blocker ONLY.
	if n, err := store.CountDependencies(ctx, "cb-child"); err != nil {
		t.Fatalf("CountDependencies: %v", err)
	} else if n != 1 {
		t.Errorf("CountDependencies(cb-child) = %d, want 1 (blocks-only; the parent-child link to cb-epic must NOT count) — matches list/ready GetDependencyCountsInTx", n)
	}

	// cb-child is depended on by cb-goal (blocks) + is the child of cb-epic
	// (parent-child edge on cb-child). dependent_count must count the blocker ONLY.
	if n, err := store.CountDependents(ctx, "cb-child"); err != nil {
		t.Fatalf("CountDependents: %v", err)
	} else if n != 1 {
		t.Errorf("CountDependents(cb-child) = %d, want 1 (blocks-only; the parent-child link from cb-epic must NOT count)", n)
	}

	// A pure parent (cb-epic) has only the parent-child edge to its child, so
	// both scalars are 0 — a hierarchy link is not a blocker in either direction.
	if n, err := store.CountDependents(ctx, "cb-epic"); err != nil {
		t.Fatalf("CountDependents(cb-epic): %v", err)
	} else if n != 0 {
		t.Errorf("CountDependents(cb-epic) = %d, want 0 (only a parent-child link exists)", n)
	}
	if n, err := store.CountDependencies(ctx, "cb-epic"); err != nil {
		t.Fatalf("CountDependencies(cb-epic): %v", err)
	} else if n != 0 {
		t.Errorf("CountDependencies(cb-epic) = %d, want 0", n)
	}
}
