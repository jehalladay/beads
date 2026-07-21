//go:build cgo

package main

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// seedMoleculeLastStepOpen creates, via the real `bd` CLI, a molecule root
// (--type molecule, which shouldAutoCloseCompletedRoot auto-closes) with two
// parent-child steps: step1 is closed, step2 stays open. It returns the root id
// and the still-open step id ("the last step"). Mirrors the parent-child edge
// mapping in batch_json_error_test.go (dep add <child> <root> --type parent-child
// maps child->IssueID, root->DependsOnID).
func seedMoleculeLastStepOpen(t *testing.T, bd, dir string) (rootID, lastStepID string) {
	t.Helper()
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

	// Close the FIRST step normally so only step2 remains — the batch below
	// closes the molecule's final step.
	closeCmd := exec.Command(bd, "close", step1.ID, "--reason", "done")
	closeCmd.Dir = dir
	closeCmd.Env = bdEnv(dir)
	if out, err := closeCmd.CombinedOutput(); err != nil {
		t.Fatalf("close step1 failed: %v\n%s", err, out)
	}

	return root.ID, step2.ID
}

// TestBatch_CloseAutoClosesCompletedMolecule is the beads-8cxe6 teeth: closing
// a molecule's FINAL step via `bd batch` must run the same
// autoCloseCompletedMolecule cascade `bd close` runs, so the molecule root
// auto-closes. batch dispatches straight against storage.Transaction, below the
// cobra handler that owns the cascade, so before the fix the root stayed OPEN —
// a silent workflow-semantic divergence from the `bd close` loop batch is a
// drop-in for (batch-parity class, beads-1d08 precedent). Mutation-verify:
// remove the post-commit cascade loop in batch.go → this test goes RED.
func TestBatch_CloseAutoClosesCompletedMolecule(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "bcm")

	rootID, lastStep := seedMoleculeLastStepOpen(t, bd, dir)

	cmd := exec.Command(bd, "batch")
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	cmd.Stdin = strings.NewReader("close " + lastStep + " done\n")
	stdout, stderr, err := runCommandBuffers(t, cmd)
	if err != nil {
		t.Fatalf("`bd batch` close last step failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}

	root := bdShow(t, bd, dir, rootID)
	if root.Status != types.StatusClosed {
		t.Errorf("molecule root %s status = %q, want %q — `bd batch close` of the final step did not auto-close the completed molecule (beads-8cxe6)\nbatch stdout:\n%s", rootID, root.Status, types.StatusClosed, stdout.String())
	}
}

// TestBatch_UpdateStatusClosedAutoClosesCompletedMolecule is the same teeth for
// the batch `update <id> status=closed` leg (beads-8cxe6): an update that
// transitions the molecule's final step to closed must also trigger the
// auto-close cascade, mirroring `bd update --status closed`.
func TestBatch_UpdateStatusClosedAutoClosesCompletedMolecule(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "bum")

	rootID, lastStep := seedMoleculeLastStepOpen(t, bd, dir)

	cmd := exec.Command(bd, "batch")
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	cmd.Stdin = strings.NewReader("update " + lastStep + " status=closed\n")
	stdout, stderr, err := runCommandBuffers(t, cmd)
	if err != nil {
		t.Fatalf("`bd batch` update status=closed failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}

	root := bdShow(t, bd, dir, rootID)
	if root.Status != types.StatusClosed {
		t.Errorf("molecule root %s status = %q, want %q — `bd batch update status=closed` of the final step did not auto-close the completed molecule (beads-8cxe6)\nbatch stdout:\n%s", rootID, root.Status, types.StatusClosed, stdout.String())
	}
}
