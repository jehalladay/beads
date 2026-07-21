//go:build cgo

package main

import (
	"bytes"
	"context"
	"os"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// beads-7v8wx: `bd ado sync` reconciliation auto-closes local beads whose ADO
// work item was deleted upstream (404) via closeReconciledDeletedIssues, which
// calls store.CloseIssue + auditStatusChange (the audit-file trail landed in
// beads-sxgz3). But it did NOT fire autoCloseCompletedMolecule after the close,
// unlike every other cmd-layer close path: bd close (close.go:223), batch
// (batch.go:283, beads-8cxe6), gate resolve/check (beads-346th), todo-done
// (beads-58kg8), human respond/dismiss (beads-rbqo8), epic (beads-4v7eb),
// duplicate (duplicate.go:210). So if the ADO-reconciled bead was a molecule's
// FINAL open step, closing it left the molecule/wisp root stranded OPEN with
// every step done — the stranded-completed-root defect the CLOSE-PARITY-MATRIX
// family exists to prevent.
//
// Uses the embedded-dolt harness (no Docker) and drives the real
// closeReconciledDeletedIssues directly against a seeded molecule, mirroring the
// sxgz3 audit-trail test and the batch_molecule_autoclose seed helper.
//
// MUTATION-VERIFY: remove the autoCloseCompletedMolecule(...) call added to
// closeReconciledDeletedIssues → this test goes RED (the final step closes but
// the molecule root stays OPEN).
func TestADOReconcileClose_AutoClosesCompletedMolecule_7v8wx(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}

	bd := buildEmbeddedBD(t)
	dir, beadsDir, _ := bdInit(t, bd, "--prefix", "adm")

	// Seed a molecule root with step1 closed and step2 (the last step) open,
	// via the real CLI — the same helper the batch autoclose teeth use.
	rootID, lastStep := seedMoleculeLastStepOpen(t, bd, dir)

	// Map the last step to a (now-deleted) ADO work item so the reconcile close
	// targets exactly the molecule's final open step.
	st := openStore(t, beadsDir, "adm")
	ctx := context.Background()

	// The audit FILE resolves relative to cwd (.beads/interactions.jsonl), so
	// chdir into the workspace before invoking the reconcile close — same as the
	// sxgz3 / autoclose family tests.
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWD) })
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir workspace: %v", err)
	}

	adoIDMap := map[int]string{7788: lastStep}
	var warnings []string
	var out bytes.Buffer

	closeReconciledDeletedIssues(ctx, st, "test-actor", []string{"7788"}, adoIDMap, &out, &warnings)

	// Precondition: the reconcile actually closed the final step (DB side).
	step, err := st.GetIssue(ctx, lastStep)
	if err != nil {
		t.Fatalf("GetIssue(%s): %v", lastStep, err)
	}
	if step == nil || step.Status != types.StatusClosed {
		var got types.Status
		if step != nil {
			got = step.Status
		}
		t.Fatalf("reconcile did not close final step %s (status=%q) — close precondition broken", lastStep, got)
	}

	// The actual invariant: closing the molecule's FINAL step via ADO reconcile
	// must cascade-close the completed molecule root, at parity with bd close /
	// batch / gate / todo / human (beads-7v8wx). Reopen a fresh store so we read
	// committed state, not the in-process view.
	verify := openStore(t, beadsDir, "adm")
	root, err := verify.GetIssue(ctx, rootID)
	if err != nil {
		t.Fatalf("GetIssue(root %s): %v", rootID, err)
	}
	if root == nil || root.Status != types.StatusClosed {
		var got types.Status
		if root != nil {
			got = root.Status
		}
		t.Errorf("REGRESSION (beads-7v8wx): molecule root %s status = %q, want %q — closing its final step via `bd ado sync` reconcile did NOT auto-close the completed molecule; the root is stranded OPEN with every step done (CLOSE-PARITY-MATRIX gap vs bd close/batch/gate/todo/human)", rootID, got, types.StatusClosed)
	}
}
