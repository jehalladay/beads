//go:build cgo

package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// beads-poj2t (batch-parity family; eth8/aw9x8 dep.add sibling): the single-dep
// path (dep.go) refuses attaching an OPEN child to a CLOSED auto-closing root
// (epic/molecule/ephemeral) via a parent-child edge — it would recreate the
// closed-parent-with-open-child inconsistency the close-guard family prevents —
// unless --force. The batch preflight (guardBatchDepAdds, batch.go) is the
// dep.add leg of that surface, the one nf1k1 (batch reopen guard) explicitly
// scoped out. Before this fix `bd batch dep.add <child> <parent> parent-child`
// wrote straight to tx.AddDependency below the CLI guard layer and silently
// landed the inconsistency (rc=0, open child under a closed root).
//
// End-to-end through the ACTUAL `bd batch` subprocess (NOT a tx-helper, which
// would false-green by skipping the CLI-layer preflight entirely — see the
// batch-parity family lessons). MUTATION-VERIFIED: removing the
// guardBatchDepAdds call lets the batch attach the open child under the closed
// root (rc=0).
func TestEmbeddedBatchDepAddClosedRootParentGuard_poj2t(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)

	// makeClosedEpicAndOpenChild builds an isolated closed epic (no children, so
	// it closes cleanly) plus a separate OPEN child not yet linked to it.
	// Returns (epicID, openChildID). The parent-child edge is what the guard
	// must refuse.
	makeClosedEpicAndOpenChild := func(t *testing.T, dir string) (string, string) {
		t.Helper()
		epic := bdCreate(t, bd, dir, "poj2t epic", "--type", "epic")
		bdClose(t, bd, dir, epic.ID) // no open children → closes cleanly
		child := bdCreate(t, bd, dir, "poj2t open child", "--type", "task")
		if got := bdShow(t, bd, dir, epic.ID); got.Status != types.StatusClosed {
			t.Fatalf("setup: epic %s should be closed, got %s", epic.ID, got.Status)
		}
		if got := bdShow(t, bd, dir, child.ID); got.Status == types.StatusClosed {
			t.Fatalf("setup: child %s should be open, got %s", child.ID, got.Status)
		}
		return epic.ID, child.ID
	}

	runBatch := func(t *testing.T, dir, stdin string, extraArgs ...string) (combined string, err error) {
		t.Helper()
		args := append([]string{"batch"}, extraArgs...)
		cmd := exec.Command(bd, args...)
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		cmd.Stdin = strings.NewReader(stdin)
		stdout, stderr, e := runCommandBuffers(t, cmd)
		return stdout.String() + stderr.String(), e
	}

	// childHasParent reports whether child has a parent-child edge to parent,
	// read from the structured `bd show --json` dependency list (robust vs a
	// substring match on rendered output).
	childHasParent := func(t *testing.T, dir, child, parent string) bool {
		t.Helper()
		for _, d := range showDeps(t, bd, dir, child) {
			if d.ID == parent && d.Type == string(types.DepParentChild) {
				return true
			}
		}
		return false
	}

	// CONTROL: single `bd dep add <child> <parent> --type parent-child` is refused
	// and no edge lands. Establishes the authoritative behavior batch must mirror.
	t.Run("single_depadd_refuses_closed_root_parent", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "pc")
		epicID, childID := makeClosedEpicAndOpenChild(t, dir)

		out := bdDepAddFail(t, bd, dir, childID, epicID, "--type", "parent-child")
		if !strings.Contains(out, "closed parent") {
			t.Errorf("expected a 'closed parent' guard error from single dep add, got:\n%s", out)
		}
		if childHasParent(t, dir, childID, epicID) {
			t.Errorf("single dep add under a closed root should NOT create the edge")
		}
	})

	// FIX: `bd batch dep.add <child> <parent> parent-child` of the same pair must
	// ALSO be refused (non-zero rc, no edge lands).
	t.Run("batch_depadd_refuses_closed_root_parent", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "pb")
		epicID, childID := makeClosedEpicAndOpenChild(t, dir)

		combined, err := runBatch(t, dir, "dep add "+childID+" "+epicID+" parent-child\n")
		if err == nil {
			t.Fatalf("expected batch dep.add under a closed root to FAIL, got rc=0:\n%s", combined)
		}
		if !strings.Contains(combined, "closed parent") {
			t.Errorf("expected a 'closed parent' guard error from batch dep.add, got:\n%s", combined)
		}
		if childHasParent(t, dir, childID, epicID) {
			t.Errorf("batch dep.add under a closed root should NOT create the edge (poj2t)")
		}
	})

	// --force override: batch --force skips the guard (parity with
	// `bd dep add ... --force`), so the edge lands.
	t.Run("batch_force_overrides_depadd_guard", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "pf")
		epicID, childID := makeClosedEpicAndOpenChild(t, dir)

		combined, err := runBatch(t, dir, "dep add "+childID+" "+epicID+" parent-child\n", "--force")
		if err != nil {
			t.Fatalf("expected batch --force to add the edge, got error: %v\n%s", err, combined)
		}
		if !childHasParent(t, dir, childID, epicID) {
			t.Errorf("batch --force should create the parent-child edge under a closed root\n%s", combined)
		}
	})

	// beads-aw9x8 widening: the guard uses isAutoClosingParentType (epic OR
	// molecule OR ephemeral), so attaching an open child under a closed MOLECULE
	// root via batch dep.add is refused too — not just epics.
	t.Run("batch_depadd_refuses_closed_molecule_root", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "pm")
		root := bdCreate(t, bd, dir, "poj2t mol root", "--type", "molecule")
		step := bdCreate(t, bd, dir, "poj2t mol step", "--type", "task")
		bdDepAdd(t, bd, dir, step.ID, root.ID, "--type", "parent-child")
		bdClose(t, bd, dir, step.ID) // last step complete → molecule root auto-closes
		if got := bdShow(t, bd, dir, root.ID); got.Status != types.StatusClosed {
			t.Fatalf("setup: molecule root %s should auto-close, got %s", root.ID, got.Status)
		}
		newChild := bdCreate(t, bd, dir, "poj2t new open child", "--type", "task")

		combined, err := runBatch(t, dir, "dep add "+newChild.ID+" "+root.ID+" parent-child\n")
		if err == nil {
			t.Fatalf("expected batch dep.add of an open child under a closed molecule to FAIL, got rc=0:\n%s", combined)
		}
		if !strings.Contains(combined, "closed parent") {
			t.Errorf("expected a 'closed parent' guard error for the molecule root, got:\n%s", combined)
		}
		if childHasParent(t, dir, newChild.ID, root.ID) {
			t.Errorf("batch dep.add under a closed molecule root should NOT create the edge (poj2t/aw9x8)")
		}
	})

	// Batch-aware: reopening the parent AND adding the child in the same batch is
	// consistent (the parent is no longer closed by commit time) → allowed.
	t.Run("batch_reopen_parent_and_depadd_child_together_allowed", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "pt")
		epicID, childID := makeClosedEpicAndOpenChild(t, dir)

		combined, err := runBatch(t, dir,
			"update "+epicID+" status=open\ndep add "+childID+" "+epicID+" parent-child\n")
		if err != nil {
			t.Fatalf("expected reopen-parent + dep.add-child together to succeed, got error: %v\n%s", err, combined)
		}
		if got := bdShow(t, bd, dir, epicID); got.Status != types.StatusOpen {
			t.Errorf("epic should be OPEN after reopening it in the same batch, got %s", got.Status)
		}
		if !childHasParent(t, dir, childID, epicID) {
			t.Errorf("the parent-child edge should land when the parent is reopened in the same batch\n%s", combined)
		}
	})

	// Batch-aware: closing the child in the same batch makes the edge consistent
	// (a closed child under a closed root is allowed) → not refused.
	t.Run("batch_depadd_and_close_child_together_allowed", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "pd")
		epicID, childID := makeClosedEpicAndOpenChild(t, dir)

		combined, err := runBatch(t, dir,
			"dep add "+childID+" "+epicID+" parent-child\nclose "+childID+"\n")
		if err != nil {
			t.Fatalf("expected dep.add + close-child together to succeed, got error: %v\n%s", err, combined)
		}
		if got := bdShow(t, bd, dir, childID); got.Status != types.StatusClosed {
			t.Errorf("child should be CLOSED after closing it in the same batch, got %s", got.Status)
		}
		if !childHasParent(t, dir, childID, epicID) {
			t.Errorf("the edge should land when the child is closed in the same batch\n%s", combined)
		}
	})

	// Negative (no false positive): attaching an open child under an OPEN epic is
	// unaffected — the common, valid parenting operation.
	t.Run("batch_depadd_under_open_root_still_allowed", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "po")
		epic := bdCreate(t, bd, dir, "poj2t open epic", "--type", "epic")
		child := bdCreate(t, bd, dir, "poj2t child open epic", "--type", "task")

		combined, err := runBatch(t, dir, "dep add "+child.ID+" "+epic.ID+" parent-child\n")
		if err != nil {
			t.Fatalf("dep.add under an OPEN epic must be allowed, got error: %v\n%s", err, combined)
		}
		if !childHasParent(t, dir, child.ID, epic.ID) {
			t.Errorf("open child under an open epic should attach\n%s", combined)
		}
	})

	// Negative (dep-type scoping): a non-parent-child edge is NOT the parenting
	// inconsistency the guard targets, so guardBatchDepAdds must never fire on it.
	// A `blocks` edge can only be task->task (an independent type-compatibility
	// rule forbids task->epic), so this isolates the type-scoping on two open
	// tasks where the target is then closed in the SAME batch — the guard would
	// only ever consider parent-child, so a blocks edge lands regardless of the
	// close.
	t.Run("batch_depadd_blocks_edge_not_guarded", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "pk")
		blocker := bdCreate(t, bd, dir, "poj2t blocker task", "--type", "task")
		target := bdCreate(t, bd, dir, "poj2t target task", "--type", "task")

		// blocker blocks target, and target is closed in the same batch. A
		// parent-child guard must not touch a `blocks` edge.
		combined, err := runBatch(t, dir,
			"dep add "+blocker.ID+" "+target.ID+" blocks\n")
		if err != nil {
			t.Fatalf("a 'blocks' edge must not be touched by the parent-child guard, got: %v\n%s", err, combined)
		}
		found := false
		for _, d := range showDeps(t, bd, dir, blocker.ID) {
			if d.ID == target.ID && d.Type == string(types.DepBlocks) {
				found = true
			}
		}
		if !found {
			t.Errorf("the 'blocks' edge should land unaffected by the parent-child guard\n%s", combined)
		}
	})
}
