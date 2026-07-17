//go:build cgo

package main

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestExpandClosedOnlyCascade_9r3f is the teeth for beads-9r3f: `bd admin
// cleanup --cascade` must stay within cleanup's closed-only contract — it may
// cascade INTO closed dependents but must NEVER pull an OPEN or PINNED dependent
// into the deletion set, even one that depends on a closed issue.
//
// Topology seeded:
//
//	closed-root
//	  ^-- open-dep      (OPEN, depends on closed-root)  -> MUST be skipped
//	  ^-- closed-dep    (CLOSED, depends on closed-root) -> MUST be included
//	         ^-- closed-grandchild (CLOSED, depends on closed-dep) -> included (recursion)
//	  ^-- pinned-dep    (CLOSED+PINNED, depends on closed-root) -> MUST be skipped
func TestExpandClosedOnlyCascade_9r3f(t *testing.T) {
	tmpDir := t.TempDir()
	store := newTestStoreWithPrefix(t, filepath.Join(tmpDir, ".beads", "beads.db"), "cc")
	ctx := context.Background()

	mk := func(id string, status types.Status, pinned bool) {
		iss := &types.Issue{
			ID: id, Title: id, Status: status,
			Priority: 1, IssueType: types.TypeTask, Pinned: pinned,
		}
		if err := store.CreateIssue(ctx, iss, "tester"); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}
	mk("closed-root", types.StatusClosed, false)
	mk("open-dep", types.StatusOpen, false)
	mk("closed-dep", types.StatusClosed, false)
	mk("closed-grandchild", types.StatusClosed, false)
	mk("pinned-dep", types.StatusClosed, true)

	dep := func(from, to string) {
		if err := store.AddDependency(ctx, &types.Dependency{
			IssueID: from, DependsOnID: to, Type: types.DepBlocks,
		}, "tester"); err != nil {
			t.Fatalf("add dep %s->%s: %v", from, to, err)
		}
	}
	dep("open-dep", "closed-root")
	dep("closed-dep", "closed-root")
	dep("closed-grandchild", "closed-dep")
	dep("pinned-dep", "closed-root")

	expanded, skipped, err := expandClosedOnlyCascade(ctx, store, []string{"closed-root"})
	if err != nil {
		t.Fatalf("expandClosedOnlyCascade: %v", err)
	}

	inExpanded := make(map[string]bool)
	for _, id := range expanded {
		inExpanded[id] = true
	}
	inSkipped := make(map[string]bool)
	for _, id := range skipped {
		inSkipped[id] = true
	}

	// Closed issues (incl. transitive) are in the deletion set.
	for _, want := range []string{"closed-root", "closed-dep", "closed-grandchild"} {
		if !inExpanded[want] {
			t.Errorf("expected %s in closed-only cascade set, got expanded=%v", want, expanded)
		}
	}
	// The OPEN dependent must NOT be deleted — this is the 9r3f fix.
	if inExpanded["open-dep"] {
		t.Errorf("REGRESSION (9r3f): open-dep is in the deletion set — cleanup cascade would delete OPEN work")
	}
	if !inSkipped["open-dep"] {
		t.Errorf("expected open-dep reported as skipped, got skipped=%v", skipped)
	}
	// A PINNED (even if closed) dependent must be protected.
	if inExpanded["pinned-dep"] {
		t.Errorf("pinned-dep is in the deletion set — pinned issues must be protected")
	}
	if !inSkipped["pinned-dep"] {
		t.Errorf("expected pinned-dep reported as skipped, got skipped=%v", skipped)
	}
}
