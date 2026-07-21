//go:build cgo

package main

import (
	"os"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestEmbeddedAutoClosingParentReopenGuard is the beads-aw9x8 teeth: the
// close-guard family (beads-2hkd/b0tw/eth8) enforces "a closed parent has no
// open children", but its type test only matched TypeEpic while the AUTO-CLOSE
// side (shouldAutoCloseCompletedRoot) closes a WIDER set — molecule roots and
// ephemeral (wisp) roots auto-close when their last step closes. So closing a
// molecule/wisp root's final step auto-closed the root, then reopening a step
// sailed past the reopen guard (rc=0), silently recreating the closed-root-with-
// open-child state the family exists to prevent.
//
// The fix shares one isAutoClosingParentType helper (epic OR molecule OR
// ephemeral) between the auto-close side and the reopen/dep-add guards so they
// cannot drift again. These are the subprocess (real `bd`) teeth — mutation:
// restore the bare `IssueType == types.TypeEpic` test in closedEpicParents
// (close.go) / the dep.go guard → the molecule + wisp cases below go from rc!=0
// back to rc=0 (RED).
func TestEmbeddedAutoClosingParentReopenGuard(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "acp")

	// seedAutoClosedRoot creates a root of the given create args (e.g. molecule
	// or --ephemeral) with a single parent-child step, then closes the step. The
	// close auto-closes the root (its only step is complete). Returns the closed
	// root and the (now-closed) step.
	seedAutoClosedRoot := func(t *testing.T, prefix string, rootArgs ...string) (root, step *types.Issue) {
		t.Helper()
		root = bdCreate(t, bd, dir, append([]string{prefix + " root"}, rootArgs...)...)
		step = bdCreate(t, bd, dir, prefix+" step", "--type", "task")
		bdDepAdd(t, bd, dir, step.ID, root.ID, "--type", "parent-child")
		bdClose(t, bd, dir, step.ID) // last step complete → root auto-closes
		if got := bdShow(t, bd, dir, root.ID); got.Status != types.StatusClosed {
			t.Fatalf("precondition: root %s should have auto-closed, got status %q", root.ID, got.Status)
		}
		if got := bdShow(t, bd, dir, step.ID); got.Status != types.StatusClosed {
			t.Fatalf("precondition: step %s should be closed, got %q", step.ID, got.Status)
		}
		return root, step
	}

	// (1) MOLECULE root: reopening a step of an auto-closed molecule root must be
	//     refused (this is the exact bug from the aw9x8 repro), overridable with
	//     --force.
	t.Run("molecule_reopen_step_refuses_without_force", func(t *testing.T) {
		_, step := seedAutoClosedRoot(t, "mol", "--type", "molecule")
		out := bdReopenFail(t, bd, dir, step.ID)
		if !strings.Contains(out, "its parent") || !strings.Contains(out, "is closed") {
			t.Errorf("expected closed-parent guard on molecule step reopen, got:\n%s", out)
		}
		if got := bdShow(t, bd, dir, step.ID); got.Status != types.StatusClosed {
			t.Errorf("step must remain closed after refused reopen, got %s", got.Status)
		}
	})

	t.Run("molecule_reopen_step_force_succeeds", func(t *testing.T) {
		_, step := seedAutoClosedRoot(t, "molf", "--type", "molecule")
		bdReopen(t, bd, dir, step.ID, "--force")
		if got := bdShow(t, bd, dir, step.ID); got.Status != types.StatusOpen {
			t.Errorf("expected step reopened with --force, got %s", got.Status)
		}
	})

	// (1b) MOLECULE root, update --status open axis (parity with reopen).
	t.Run("molecule_update_status_open_refuses_without_force", func(t *testing.T) {
		_, step := seedAutoClosedRoot(t, "molu", "--type", "molecule")
		out := bdUpdateFail(t, bd, dir, step.ID, "--status", "open")
		if !strings.Contains(out, "its parent") || !strings.Contains(out, "is closed") {
			t.Errorf("expected closed-parent guard on molecule step update --status open, got:\n%s", out)
		}
		if got := bdShow(t, bd, dir, step.ID); got.Status != types.StatusClosed {
			t.Errorf("step must remain closed after refused update, got %s", got.Status)
		}
	})

	// (2) EPHEMERAL (wisp) root: the same guard must fire for a CLOSED ephemeral
	//     root. shouldAutoCloseCompletedRoot returns true for Ephemeral roots, so
	//     production wisp-molecule flows auto-close the root; here we reach the
	//     same closed-parent state by an explicit root close (the epic-close guard
	//     at close.go:159 is epic-only, so an ephemeral root closes freely even
	//     with an open child — exactly the gap that makes the reopen guard the
	//     load-bearing one). isAutoClosingParentType's Ephemeral branch is what
	//     this exercises. Mutation: drop `|| issue.Ephemeral` from the helper →
	//     this goes RED.
	t.Run("wisp_reopen_step_refuses_without_force", func(t *testing.T) {
		root := bdCreate(t, bd, dir, "wisp root", "--type", "task", "--ephemeral")
		step := bdCreate(t, bd, dir, "wisp step", "--type", "task")
		bdDepAdd(t, bd, dir, step.ID, root.ID, "--type", "parent-child")
		bdClose(t, bd, dir, step.ID)
		bdClose(t, bd, dir, root.ID) // ephemeral root closes freely (not epic-guarded)
		if got := bdShow(t, bd, dir, root.ID); got.Status != types.StatusClosed {
			t.Fatalf("precondition: ephemeral root should be closed, got %s", got.Status)
		}
		out := bdReopenFail(t, bd, dir, step.ID)
		if !strings.Contains(out, "its parent") || !strings.Contains(out, "is closed") {
			t.Errorf("expected closed-parent guard on wisp step reopen, got:\n%s", out)
		}
		if got := bdShow(t, bd, dir, step.ID); got.Status != types.StatusClosed {
			t.Errorf("step must remain closed after refused reopen, got %s", got.Status)
		}
	})

	// (3) dep-add guard: attaching an OPEN child to a closed molecule root must be
	//     refused (the eth8 sibling axis), overridable with --force.
	t.Run("dep_add_open_child_to_closed_molecule_refuses", func(t *testing.T) {
		root, _ := seedAutoClosedRoot(t, "moldep", "--type", "molecule")
		openChild := bdCreate(t, bd, dir, "moldep new child", "--type", "task") // open
		out := bdDepAddFail(t, bd, dir, openChild.ID, root.ID, "--type", "parent-child")
		if !strings.Contains(out, "closed parent") {
			t.Errorf("expected closed-parent dep-add guard for molecule root, got:\n%s", out)
		}
		// The edge must not have been added (guard fired before AddDependency).
		for _, d := range showDeps(t, bd, dir, openChild.ID) {
			if d.ID == root.ID {
				t.Errorf("parent-child edge to closed molecule should not have been added: %+v", d)
			}
		}
	})

	t.Run("dep_add_open_child_to_closed_molecule_force_succeeds", func(t *testing.T) {
		root, _ := seedAutoClosedRoot(t, "moldepf", "--type", "molecule")
		openChild := bdCreate(t, bd, dir, "moldepf new child", "--type", "task")
		bdDepAdd(t, bd, dir, openChild.ID, root.ID, "--type", "parent-child", "--force")
	})

	// (4) REGRESSION CONTROL: a plain (non-template, non-molecule) epic that stays
	//     OPEN when its child closes is unaffected — reopening the child is fine,
	//     because the epic is still OPEN (guard only fires on a CLOSED parent). The
	//     fix must not start blocking legitimate reopens.
	t.Run("open_epic_parent_child_reopen_unaffected", func(t *testing.T) {
		epic := bdCreate(t, bd, dir, "plain epic", "--type", "epic")
		child := bdCreate(t, bd, dir, "epic child", "--type", "task")
		bdDepAdd(t, bd, dir, child.ID, epic.ID, "--type", "parent-child")
		bdClose(t, bd, dir, child.ID)
		// Plain epic does NOT auto-close, so it stays open.
		if got := bdShow(t, bd, dir, epic.ID); got.Status == types.StatusClosed {
			t.Fatalf("precondition: plain epic must stay open when its child closes, got %s", got.Status)
		}
		// Reopening the child under an OPEN epic parent must succeed.
		bdReopen(t, bd, dir, child.ID)
		if got := bdShow(t, bd, dir, child.ID); got.Status != types.StatusOpen {
			t.Errorf("reopen of child under an OPEN epic should succeed, got %s", got.Status)
		}
	})
}
