//go:build cgo

package main

import (
	"os/exec"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// beads-zt47w: autoCloseCompletedMolecule (close.go) closes the molecule ROOT
// via s.CloseIssue directly, which records only the DB EventClosed row — NOT the
// GC-survivable .beads/interactions.jsonl audit-file trail (beads-n4sn) that
// every explicit close writes via auditStatusChange. So the AUTO-closed root's
// close vanished from the durable record after a Dolt GC flatten while the
// directly-closed step's did not — the same n4sn divergence r3m8v fixed for the
// supersede/duplicate SOURCE, one hop downstream in the cascade. The fix emits
// auditStatusChange for the root right after the successful CloseIssue at the
// single chokepoint (covers bd close / bd duplicate / bd supersede).
//
// End-to-end through the real `bd` subprocess: the cwd-based audit FILE is only
// written by the real cmd handler (c2pr1 lesson). Reuses seedMoleculeLastStepOpen
// + auditHasStatusChange from the sibling family tests.
//
// MUTATION-VERIFIED: removing the auditStatusChange(moleculeID, ...) call in
// autoCloseCompletedMolecule → TestAutoCloseRoot_WritesGCSurvivableAuditTrail_zt47w
// goes RED (root closes in the DB but no file-trail entry).

func TestAutoCloseRoot_WritesGCSurvivableAuditTrail_zt47w(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "acr")

	rootID, lastStep := seedMoleculeLastStepOpen(t, bd, dir)

	// Close the molecule's FINAL step via plain `bd close` — this triggers the
	// autoCloseCompletedMolecule cascade on the root.
	cmd := exec.Command(bd, "close", lastStep, "--reason", "done")
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	stdout, stderr, err := runCommandBuffers(t, cmd)
	if err != nil {
		t.Fatalf("`bd close` last step failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}

	// Precondition: the root actually auto-closed (DB side).
	if root := bdShow(t, bd, dir, rootID); root.Status != types.StatusClosed {
		t.Fatalf("molecule root %s did not auto-close (status=%q) — cascade precondition broken\nstdout:\n%s", rootID, root.Status, stdout.String())
	}

	// The auto-closed ROOT must get its own GC-survivable field_change entry, at
	// parity with the directly-closed step (beads-zt47w).
	if !auditHasStatusChange(t, dir, rootID, "closed") {
		t.Errorf("auto-closed molecule root %s has NO GC-survivable audit field_change to status=closed (beads-zt47w) — the cascade close is invisible after a Dolt GC flatten while the step's close is not", rootID)
	}
	// Sanity: the step itself still records its close (the pre-existing n4sn path).
	if !auditHasStatusChange(t, dir, lastStep, "closed") {
		t.Fatalf("step %s missing its own audit field_change — harness/baseline broken", lastStep)
	}
}

// Negative: closing a NON-final step must not auto-close the root, so no root
// field_change should appear prematurely.
func TestAutoCloseRoot_NotYetComplete_NoPrematureRootAudit_zt47w(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "acn")

	root := bdCreate(t, bd, dir, "molecule root", "--type", "molecule")
	step1 := bdCreate(t, bd, dir, "step 1", "--type", "task")
	step2 := bdCreate(t, bd, dir, "step 2", "--type", "task")
	for _, stepID := range []string{step1.ID, step2.ID} {
		depCmd := exec.Command(bd, "dep", "add", stepID, root.ID, "--type", "parent-child")
		depCmd.Dir = dir
		depCmd.Env = bdEnv(dir)
		if out, err := depCmd.CombinedOutput(); err != nil {
			t.Fatalf("dep add %s -> %s failed: %v\n%s", stepID, root.ID, err, out)
		}
	}

	// Close only ONE of two steps — molecule is NOT complete.
	cmd := exec.Command(bd, "close", step1.ID, "--reason", "partial")
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	if _, stderr, err := runCommandBuffers(t, cmd); err != nil {
		t.Fatalf("`bd close` step1 failed: %v\nstderr:\n%s", err, stderr.String())
	}

	if r := bdShow(t, bd, dir, root.ID); r.Status == types.StatusClosed {
		t.Fatalf("root %s closed after only one of two steps — cascade fired prematurely", root.ID)
	}
	if auditHasStatusChange(t, dir, root.ID, "closed") {
		t.Errorf("root %s got a premature audit field_change to closed before the molecule completed (beads-zt47w)", root.ID)
	}
}
