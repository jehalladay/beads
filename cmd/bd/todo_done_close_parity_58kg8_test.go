//go:build cgo

package main

import (
	"os/exec"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// beads-58kg8: `bd todo done` documents itself as a convenience wrapper for `bd
// close` (todo.go help: "bd todo done <id> -> bd close <id>"), but its RunE
// called store.CloseIssue directly and DROPPED the two post-close steps `bd
// close` runs per item (close.go:213-223):
//
//  1. auditStatusChange — the GC-survivable .beads/interactions.jsonl trail
//     (beads-n4sn) that survives a Dolt GC flatten; and
//  2. autoCloseCompletedMolecule — the completed-molecule/wisp/template-epic
//     auto-close cascade (beads-26gea/zzp26/8cxe6 family).
//
// So closing a molecule's FINAL step via `bd todo done` left the auto-closing
// root stuck OPEN (orphaned-completed-root) and dropped the durable close audit,
// while `bd close` of the same step did both. These are the DIRECT teeth (todo
// done has no proxied handling — it uses getStore()). End-to-end through the
// real `bd` subprocess: the cwd-based audit FILE is only written by the real cmd
// handler (c2pr1 lesson). Reuses seedMoleculeLastStepOpen + auditHasStatusChange
// from the sibling family tests.
//
// MUTATION-VERIFIED: removing the autoCloseCompletedMolecule call in todo.go →
// TestTodoDone_AutoClosesCompletedMolecule_58kg8 goes RED; removing the
// auditStatusChange call → TestTodoDone_WritesGCSurvivableAuditTrail_58kg8 goes
// RED.

func TestTodoDone_AutoClosesCompletedMolecule_58kg8(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "tdm")

	rootID, lastStep := seedMoleculeLastStepOpen(t, bd, dir)

	cmd := exec.Command(bd, "todo", "done", lastStep)
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	stdout, stderr, err := runCommandBuffers(t, cmd)
	if err != nil {
		t.Fatalf("`bd todo done` last step failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}

	root := bdShow(t, bd, dir, rootID)
	if root.Status != types.StatusClosed {
		t.Errorf("molecule root %s status = %q, want %q — `bd todo done` of the final step did not auto-close the completed molecule (beads-58kg8)\ntodo done stdout:\n%s", rootID, root.Status, types.StatusClosed, stdout.String())
	}
}

func TestTodoDone_WritesGCSurvivableAuditTrail_58kg8(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "tda")

	// CONTROL: a plain `bd close` writes the status audit trail — proves the
	// harness detects the entry a single-path close emits.
	ctrl := bdCreate(t, bd, dir, "control close", "--type", "task")
	ccmd := exec.Command(bd, "close", ctrl.ID, "-r", "control")
	ccmd.Dir = dir
	ccmd.Env = bdEnv(dir)
	if _, stderr, err := runCommandBuffers(t, ccmd); err != nil {
		t.Fatalf("CONTROL bd close %s failed: %v\nstderr:\n%s", ctrl.ID, err, stderr.String())
	}
	if !auditHasStatusChange(t, dir, ctrl.ID, "closed") {
		t.Fatalf("CONTROL: single-path close did not write a status field_change for %s — harness broken", ctrl.ID)
	}

	// TEST: `bd todo done` must write the SAME GC-survivable audit trail.
	todo := bdCreate(t, bd, dir, "a todo item", "--type", "task")
	cmd := exec.Command(bd, "todo", "done", todo.ID)
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	if stdout, stderr, err := runCommandBuffers(t, cmd); err != nil {
		t.Fatalf("`bd todo done` failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	if bdShow(t, bd, dir, todo.ID).Status != types.StatusClosed {
		t.Fatalf("`bd todo done` did not close %s", todo.ID)
	}
	if !auditHasStatusChange(t, dir, todo.ID, "closed") {
		t.Errorf("`bd todo done` did not write a GC-survivable audit field_change to status=closed for %s (beads-58kg8) — parity with single-path close broken", todo.ID)
	}
}
