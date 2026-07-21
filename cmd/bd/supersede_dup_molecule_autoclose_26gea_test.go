//go:build cgo

package main

import (
	"os/exec"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// beads-26gea: `bd supersede`/`bd duplicate` close the source issue via
// store.LinkAndClose but, like `bd update --status closed` (beads-zzp26) and
// `bd batch` (beads-8cxe6), bypass the cmd-layer completed-molecule auto-close
// cascade `bd close` runs (close.go:223). So superseding/duplicating a
// molecule's FINAL open step silently left the auto-closing root stuck OPEN.
// These are the DIRECT legs (the proxied legs run through
// runLinkAndCloseProxied, tested by the *_proxied_* siblings under
// BEADS_TEST_PROXIED_SERVER=1). End-to-end through the real `bd` subprocess.
// MUTATION-VERIFIED: removing the autoCloseCompletedMolecule call in the
// respective duplicate.go leg leaves the root OPEN → the leg's test goes RED.

func TestSupersede_AutoClosesCompletedMolecule_26gea(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "sam")

	rootID, lastStep := seedMoleculeLastStepOpen(t, bd, dir)
	replacement := bdCreate(t, bd, dir, "replacement", "--type", "task")

	// Supersede the molecule's final step → it closes the step; the root must
	// then auto-close (all steps complete).
	cmd := exec.Command(bd, "supersede", lastStep, "--with", replacement.ID)
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	stdout, stderr, err := runCommandBuffers(t, cmd)
	if err != nil {
		t.Fatalf("`bd supersede` of last step failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}

	if root := bdShow(t, bd, dir, rootID); root.Status != types.StatusClosed {
		t.Errorf("molecule root %s status = %q, want %q — `bd supersede` of the final step did not auto-close the completed molecule (beads-26gea)\nsupersede stdout:\n%s", rootID, root.Status, types.StatusClosed, stdout.String())
	}
}

func TestDuplicate_AutoClosesCompletedMolecule_26gea(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "dam")

	rootID, lastStep := seedMoleculeLastStepOpen(t, bd, dir)
	canonical := bdCreate(t, bd, dir, "canonical", "--type", "task")

	// Mark the molecule's final step a duplicate of the canonical → it closes
	// the step; the root must then auto-close.
	cmd := exec.Command(bd, "duplicate", lastStep, "--of", canonical.ID)
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	stdout, stderr, err := runCommandBuffers(t, cmd)
	if err != nil {
		t.Fatalf("`bd duplicate` of last step failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}

	if root := bdShow(t, bd, dir, rootID); root.Status != types.StatusClosed {
		t.Errorf("molecule root %s status = %q, want %q — `bd duplicate` of the final step did not auto-close the completed molecule (beads-26gea)\nduplicate stdout:\n%s", rootID, root.Status, types.StatusClosed, stdout.String())
	}
}

// Negative (no false positive): superseding a NON-final step must NOT auto-close
// the root — the molecule is not complete yet. Pins the cascade to real
// completion, matching `bd close`.
func TestSupersede_NonFinalStepDoesNotAutoCloseRoot_26gea(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "san")

	// Molecule root with two OPEN steps; supersede ONE → other stays open → root
	// must stay open.
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
	replacement := bdCreate(t, bd, dir, "replacement", "--type", "task")

	cmd := exec.Command(bd, "supersede", step1.ID, "--with", replacement.ID)
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	stdout, stderr, err := runCommandBuffers(t, cmd)
	if err != nil {
		t.Fatalf("`bd supersede` of a non-final step failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}

	if root := bdShow(t, bd, dir, root.ID); root.Status == types.StatusClosed {
		t.Errorf("molecule root %s auto-closed after superseding only ONE of two steps — the cascade must fire only on real completion (beads-26gea)\nsupersede stdout:\n%s", root.ID, stdout.String())
	}
}
