//go:build cgo

package main

import (
	"os"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestEmbeddedUpdateReparentAtomic is the teeth for beads-gkq1c: the DIRECT
// `bd update --parent` reparent path removed the old parent edge(s) and added
// the new parent as SEPARATE autocommits with no enclosing transaction. If the
// new-parent AddDependency failed AFTER the removes (e.g. it would form a cycle,
// or the parent vanished, or a storage error), the child was ORPHANED — a single
// logical reparent silently DROPPED the existing parent instead of leaving state
// unchanged. The already-atomic proxied twin (update_proxied_server.go, one
// uw.Commit) never had this. Fix wraps remove+add in RunInTransaction so a failed
// add rolls back the removes and the child keeps its original parent.
//
// RED-proven repro (from the bead): A=epic, C=child-of-A, D=child-of-C. Then
// `bd update C --parent D` is correctly rejected (C->D->C cycle) — but the
// non-atomic path left C with NO parent-child dependency (old C->A edge already
// deleted, new C->D add failed cycle-check, nothing rolled back).
//
// The assertion reads C's OWN dependency records (showDeps), NOT `bd children A`:
// `children` renders by hierarchical ID prefix, so C (id A.1) still displays under
// A even when the actual C->A dependency edge is deleted — it does NOT reflect the
// orphan. The dependency record is ground truth. Mutation: reverting the reparent
// block to the non-transactional remove-then-add turns the "still has its parent
// dep" assertion RED (verified: C ends with zero parent-child deps).
func TestEmbeddedUpdateReparentAtomic(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)

	// parentChildDeps returns the parent IDs (DependsOnID) of the issue's
	// parent-child dependency records — the true parent edges, from the child's
	// own dependency list (not the prefix-based `bd children` render).
	parentChildDeps := func(t *testing.T, dir, id string) []string {
		t.Helper()
		var parents []string
		for _, d := range showDeps(t, bd, dir, id) {
			if d.Type == string(types.DepParentChild) {
				parents = append(parents, d.ID)
			}
		}
		return parents
	}

	// (1) THE TEETH: a failed reparent (cycle) must NOT orphan the child — its
	//     original parent-child dependency survives because the whole reparent is
	//     one transaction that rolls back on the failed add.
	t.Run("failed_cycle_reparent_preserves_original_parent", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "ra")
		a := bdCreate(t, bd, dir, "A epic", "--type", "epic")
		c := bdCreate(t, bd, dir, "C child of A", "--type", "task", "--parent", a.ID)
		d := bdCreate(t, bd, dir, "D child of C", "--type", "task", "--parent", c.ID)

		// Sanity: C's parent edge points at A before the reparent attempt.
		if got := parentChildDeps(t, dir, c.ID); len(got) != 1 || got[0] != a.ID {
			t.Fatalf("setup: C %s should have exactly one parent-child dep to A %s, got %v", c.ID, a.ID, got)
		}

		// `update C --parent D` forms the cycle C->D->C and must be rejected.
		out := bdUpdateFail(t, bd, dir, c.ID, "--parent", d.ID)
		if !strings.Contains(out, "cycle") {
			t.Errorf("expected a cycle rejection reparenting C under its own descendant D, got:\n%s", out)
		}

		// THE ORPHAN CHECK: after the rejected reparent, C must STILL have its
		// parent-child dep to A (transaction rolled back the old-edge removal).
		// A non-atomic path already deleted C->A → C ends up with zero parents.
		got := parentChildDeps(t, dir, c.ID)
		if len(got) != 1 || got[0] != a.ID {
			t.Errorf("gkq1c: after a FAILED reparent, C %s must still have its parent-child dep to A %s (not orphaned); got parents %v", c.ID, a.ID, got)
		}
	})

	// (2) A successful reparent still moves the child (regression control): the
	//     transaction commits the remove+add, so C leaves the old parent and joins
	//     the new one — exactly one parent-child dep, now to B.
	t.Run("successful_reparent_moves_child", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "rb")
		a := bdCreate(t, bd, dir, "A epic", "--type", "epic")
		b := bdCreate(t, bd, dir, "B epic", "--type", "epic")
		c := bdCreate(t, bd, dir, "C child", "--type", "task", "--parent", a.ID)

		bdUpdate(t, bd, dir, c.ID, "--parent", b.ID)

		got := parentChildDeps(t, dir, c.ID)
		if len(got) != 1 || got[0] != b.ID {
			t.Errorf("after successful reparent, C %s should have exactly one parent-child dep to B %s, got %v", c.ID, b.ID, got)
		}
	})

	// NOTE: --parent "" (remove parent) atomicity within the same transaction is
	// already covered by TestEmbeddedUpdate/update_parent_remove (update_embedded_test.go),
	// which also asserts via the child's own dependency records; not duplicated here.
}
