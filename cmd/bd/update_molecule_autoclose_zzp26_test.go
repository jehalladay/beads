//go:build cgo

package main

import (
	"os/exec"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestUpdate_StatusClosedAutoClosesCompletedMolecule is the beads-zzp26 teeth:
// closing a molecule's FINAL step via the single `bd update <id> --status
// closed` must run the same autoCloseCompletedMolecule cascade `bd close` runs,
// so the molecule root auto-closes. `bd update --status closed` reaches the SAME
// terminal close as `bd close` (whose help it mirrors) but wrote straight to
// UpdateIssue without the post-close cascade, so closing a molecule's last step
// via update silently left the root stuck OPEN — an auto-close-parity gap and
// the SINGLE-update sibling of the batch leg (beads-8cxe6) and the sup/dup leg
// (beads-26gea). Uses the shared seedMoleculeLastStepOpen helper (a molecule
// root + 2 parent-child steps, step1 pre-closed).
//
// MUTATION-VERIFIED: removing the post-commit closedSteps cascade in update.go
// leaves the root OPEN → this test goes RED.
func TestUpdate_StatusClosedAutoClosesCompletedMolecule(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "uzm")

	rootID, lastStep := seedMoleculeLastStepOpen(t, bd, dir)

	// Single `bd update <step> --status closed` of the molecule's final step.
	cmd := exec.Command(bd, "update", lastStep, "--status", "closed")
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	stdout, stderr, err := runCommandBuffers(t, cmd)
	if err != nil {
		t.Fatalf("`bd update --status closed` of last step failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}

	if root := bdShow(t, bd, dir, rootID); root.Status != types.StatusClosed {
		t.Errorf("molecule root %s status = %q, want %q — `bd update --status closed` of the final step did not auto-close the completed molecule (beads-zzp26)\nupdate stdout:\n%s", rootID, root.Status, types.StatusClosed, stdout.String())
	}

	// The closed step must itself be closed (the update applied).
	if step := bdShow(t, bd, dir, lastStep); step.Status != types.StatusClosed {
		t.Errorf("last step %s status = %q, want closed — the update itself did not apply", lastStep, step.Status)
	}
}

// TestUpdate_StatusClosedNonFinalStepDoesNotAutoCloseRoot is the negative
// (no false positive): closing a NON-final step via `bd update --status closed`
// must NOT auto-close the root, because the molecule is not yet complete. This
// pins the cascade to real completion (shouldAutoCloseCompletedRoot + progress),
// matching `bd close`.
func TestUpdate_StatusClosedNonFinalStepDoesNotAutoCloseRoot(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "uzn")

	// A molecule root with two OPEN steps (nothing pre-closed): closing ONE via
	// update leaves the other open → root must stay open.
	root := bdCreate(t, bd, dir, "molecule root", "--type", "molecule")
	step1 := bdCreate(t, bd, dir, "step 1", "--type", "task")
	step2 := bdCreate(t, bd, dir, "step 2", "--type", "task")
	for _, stepID := range []string{step1.ID, step2.ID} {
		depCmd := exec.Command(bd, "dep", "add", stepID, root.ID, "--type", "parent-child")
		depCmd.Dir = dir
		depCmd.Env = bdEnv(dir)
		if out, err := depCmd.CombinedOutput(); err != nil {
			t.Fatalf("dep add (parent-child) %s -> %s failed: %v\n%s", stepID, root.ID, err, out)
		}
	}

	cmd := exec.Command(bd, "update", step1.ID, "--status", "closed")
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	stdout, stderr, err := runCommandBuffers(t, cmd)
	if err != nil {
		t.Fatalf("`bd update --status closed` of a non-final step failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}

	if root := bdShow(t, bd, dir, root.ID); root.Status == types.StatusClosed {
		t.Errorf("molecule root %s auto-closed after closing only ONE of two steps — the cascade must fire only on real completion (beads-zzp26)\nupdate stdout:\n%s", root.ID, stdout.String())
	}
}
