//go:build cgo

package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// beads-nf1k1 (batch-parity family, SWEEP-201; b0tw-family sibling): the
// single-update path (update.go) and `bd reopen` refuse reopening a closed
// child whose parent epic is itself closed (status->open would recreate the
// closed-epic-with-open-child state the close-guard family exists to prevent) —
// unless --force. The batch preflight (guardBatchReopens, batch.go) is the third
// leg of that surface; before this fix `bd batch update <child> status=open`
// wrote straight to tx.UpdateIssue below the CLI guard layer and silently
// landed the inconsistency (rc=0, child OPEN, epic still CLOSED).
//
// End-to-end through the ACTUAL `bd batch` subprocess (NOT a tx-helper, which
// would false-green by skipping the CLI-layer preflight entirely — see the
// batch-parity family lessons). MUTATION-VERIFIED: removing the
// guardBatchReopens call (or its closedEpicParents check) lets the batch reopen
// the child under the closed epic (rc=0, child OPEN).
func TestEmbeddedBatchReopenClosedEpicParentGuard_nf1k1(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)

	// makeClosedChildUnderClosedEpic builds the b0tw scenario: a closed child
	// with a parent-child edge to a closed epic. Returns (epicID, childID).
	// Close order matters: close the child FIRST so the epic has no open child
	// when it is closed (the epic-close guard would otherwise refuse).
	makeClosedChildUnderClosedEpic := func(t *testing.T, dir string) (string, string) {
		t.Helper()
		epic := bdCreate(t, bd, dir, "nf1k1 epic", "--type", "epic")
		child := bdCreate(t, bd, dir, "nf1k1 child", "--type", "task", "--parent", epic.ID)
		bdClose(t, bd, dir, child.ID)
		bdClose(t, bd, dir, epic.ID)
		if got := bdShow(t, bd, dir, child.ID); got.Status != types.StatusClosed {
			t.Fatalf("setup: child %s should be closed, got %s", child.ID, got.Status)
		}
		if got := bdShow(t, bd, dir, epic.ID); got.Status != types.StatusClosed {
			t.Fatalf("setup: epic %s should be closed, got %s", epic.ID, got.Status)
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

	// CONTROL: single `bd update <child> --status open` is refused and the child
	// stays closed. Establishes the authoritative behavior batch must mirror.
	t.Run("single_update_reopen_refuses_closed_epic_parent", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "nc")
		_, childID := makeClosedChildUnderClosedEpic(t, dir)

		out := bdUpdateFail(t, bd, dir, childID, "--status", "open")
		if !strings.Contains(out, "closed") {
			t.Errorf("expected a 'closed epic' guard error from single update, got:\n%s", out)
		}
		if got := bdShow(t, bd, dir, childID); got.Status != types.StatusClosed {
			t.Errorf("single reopen under a closed epic should leave the child CLOSED, got %s", got.Status)
		}
	})

	// FIX: `bd batch update <child> status=open` of the same child must ALSO be
	// refused (non-zero rc, child stays closed).
	t.Run("batch_update_reopen_refuses_closed_epic_parent", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "nb")
		_, childID := makeClosedChildUnderClosedEpic(t, dir)

		combined, err := runBatch(t, dir, "update "+childID+" status=open\n")
		if err == nil {
			t.Fatalf("expected batch reopen to FAIL under a closed epic, got rc=0:\n%s", combined)
		}
		if !strings.Contains(combined, "closed") {
			t.Errorf("expected a 'closed epic' guard error from batch reopen, got:\n%s", combined)
		}
		if got := bdShow(t, bd, dir, childID); got.Status != types.StatusClosed {
			t.Errorf("batch reopen under a closed epic should leave the child CLOSED (nf1k1), got %s", got.Status)
		}
	})

	// --force override: batch --force skips the guard (parity with
	// `bd update --status open --force`), so the child reopens.
	t.Run("batch_force_overrides_reopen_guard", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "nf")
		_, childID := makeClosedChildUnderClosedEpic(t, dir)

		combined, err := runBatch(t, dir, "update "+childID+" status=open\n", "--force")
		if err != nil {
			t.Fatalf("expected batch --force to reopen the child, got error: %v\n%s", err, combined)
		}
		if got := bdShow(t, bd, dir, childID); got.Status != types.StatusOpen {
			t.Errorf("batch --force should reopen the child under a closed epic, got %s\n%s", got.Status, combined)
		}
	})

	// Batch-aware: reopening the epic AND the child in the same batch is
	// consistent (the parent is no longer closed by commit time) → allowed.
	t.Run("batch_reopen_epic_and_child_together_allowed", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "nt")
		epicID, childID := makeClosedChildUnderClosedEpic(t, dir)

		combined, err := runBatch(t, dir, "update "+epicID+" status=open\nupdate "+childID+" status=open\n")
		if err != nil {
			t.Fatalf("expected batch reopen of epic+child together to succeed, got error: %v\n%s", err, combined)
		}
		if got := bdShow(t, bd, dir, childID); got.Status != types.StatusOpen {
			t.Errorf("child should be OPEN after reopening epic+child together, got %s", got.Status)
		}
		if got := bdShow(t, bd, dir, epicID); got.Status != types.StatusOpen {
			t.Errorf("epic should be OPEN after reopening epic+child together, got %s", got.Status)
		}
	})

	// beads-aw9x8 widened the closed-parent reopen guard from epic-only to the
	// full auto-closing set (epic OR molecule OR ephemeral) via the shared
	// isAutoClosingParentType helper, which closedEpicParents now consumes. Since
	// guardBatchReopens calls closedEpicParents, the batch path inherits that
	// widening for free — this subtest proves it: reopening a step of an
	// auto-closed MOLECULE root via `bd batch update status=open` must be refused
	// (the exact class aw9x8 exists to protect, through the batch leg).
	t.Run("batch_reopen_molecule_step_refuses", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "nm")
		root := bdCreate(t, bd, dir, "nf1k1 mol root", "--type", "molecule")
		step := bdCreate(t, bd, dir, "nf1k1 mol step", "--type", "task")
		bdDepAdd(t, bd, dir, step.ID, root.ID, "--type", "parent-child")
		bdClose(t, bd, dir, step.ID) // last step complete → molecule root auto-closes
		if got := bdShow(t, bd, dir, root.ID); got.Status != types.StatusClosed {
			t.Fatalf("setup: molecule root %s should auto-close, got %s", root.ID, got.Status)
		}
		if got := bdShow(t, bd, dir, step.ID); got.Status != types.StatusClosed {
			t.Fatalf("setup: step %s should be closed, got %s", step.ID, got.Status)
		}

		combined, err := runBatch(t, dir, "update "+step.ID+" status=open\n")
		if err == nil {
			t.Fatalf("expected batch reopen of a molecule step to FAIL, got rc=0:\n%s", combined)
		}
		if !strings.Contains(combined, "closed") {
			t.Errorf("expected a 'closed parent' guard error for molecule step, got:\n%s", combined)
		}
		if got := bdShow(t, bd, dir, step.ID); got.Status != types.StatusClosed {
			t.Errorf("batch reopen of a molecule step should leave it CLOSED (nf1k1/aw9x8), got %s", got.Status)
		}
	})

	// Negative (no false positive): reopening a closed child under an OPEN epic
	// is unaffected.
	t.Run("batch_reopen_under_open_epic_still_allowed", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "no")
		epic := bdCreate(t, bd, dir, "nf1k1 open epic", "--type", "epic")
		child := bdCreate(t, bd, dir, "nf1k1 child open epic", "--type", "task", "--parent", epic.ID)
		bdClose(t, bd, dir, child.ID) // child closed, epic stays OPEN
		if got := bdShow(t, bd, dir, child.ID); got.Status != types.StatusClosed {
			t.Fatalf("setup: child should be closed, got %s", got.Status)
		}

		combined, err := runBatch(t, dir, "update "+child.ID+" status=open\n")
		if err != nil {
			t.Fatalf("reopen under an OPEN epic must be allowed, got error: %v\n%s", err, combined)
		}
		if got := bdShow(t, bd, dir, child.ID); got.Status != types.StatusOpen {
			t.Errorf("child under an open epic should reopen, got %s", got.Status)
		}
	})
}
