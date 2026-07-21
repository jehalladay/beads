//go:build cgo

package main

import (
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// beads-9y2f3 (PROXIED twin of beads-zzp26): the proxied `bd update --status
// closed` path (update_proxied_server.go, applyUpdateProxiedOne → ApplyUpdate)
// closed the step but bypassed the completed-molecule auto-close cascade that
// the DIRECT update path (beads-zzp26) and the proxied CLOSE path both run. So
// on a hub-connected sql-server crew, `bd update <final-step> --status closed`
// left the auto-closing molecule root stuck OPEN. Runs end-to-end through the
// real proxied-server subprocess (BEADS_TEST_PROXIED_SERVER=1).
// MUTATION-VERIFIED: removing the autoCloseProxiedCompletedMolecule call in
// applyUpdateProxiedOne (or moving it after uw.Commit so the staged root-close
// is rolled back) leaves the root OPEN.
//
// seedProxiedMoleculeLastStep9y2f3 builds, via the real proxied `bd`, a molecule
// root with two parent-child steps (step1 pre-closed, step2 open) and returns
// the root id + still-open last-step id. Self-contained (uniquely named) so this
// file compiles independently of the 26gea proxied sibling's helper regardless
// of land order.
func seedProxiedMoleculeLastStep9y2f3(t *testing.T, bd string, p proxiedProject) (rootID, lastStepID string) {
	t.Helper()
	root := bdProxiedCreate(t, bd, p.dir, "molecule root", "--type", "molecule")
	step1 := bdProxiedCreate(t, bd, p.dir, "step 1", "--type", "task")
	step2 := bdProxiedCreate(t, bd, p.dir, "step 2", "--type", "task")
	for _, stepID := range []string{step1.ID, step2.ID} {
		if out, err := bdProxiedRun(t, bd, p.dir, "dep", "add", stepID, root.ID, "--type", "parent-child"); err != nil {
			t.Fatalf("proxied dep add %s -> %s failed: %v\n%s", stepID, root.ID, err, out)
		}
	}
	if out, err := bdProxiedRun(t, bd, p.dir, "close", step1.ID, "--reason", "done"); err != nil {
		t.Fatalf("proxied close step1 failed: %v\n%s", err, out)
	}
	return root.ID, step2.ID
}

func TestProxiedUpdate_StatusClosedAutoClosesCompletedMolecule_9y2f3(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)
	p := bdProxiedInit(t, bd, "pum")

	rootID, lastStep := seedProxiedMoleculeLastStep9y2f3(t, bd, p)

	if out, err := bdProxiedRun(t, bd, p.dir, "update", lastStep, "--status", "closed"); err != nil {
		t.Fatalf("proxied `bd update --status closed` of last step failed: %v\n%s", err, out)
	}

	if root := bdProxiedShow(t, bd, p.dir, rootID); root.Status != types.StatusClosed {
		t.Errorf("molecule root %s status = %q, want %q — proxied `bd update --status closed` of the final step did not auto-close the completed molecule (beads-9y2f3)", rootID, root.Status, types.StatusClosed)
	}
	// The step itself must be closed (the update applied).
	if step := bdProxiedShow(t, bd, p.dir, lastStep); step.Status != types.StatusClosed {
		t.Errorf("last step %s status = %q, want closed — the update itself did not apply", lastStep, step.Status)
	}
}

// Negative (no false positive): proxied update of a NON-final step must NOT
// auto-close the root.
func TestProxiedUpdate_StatusClosedNonFinalStepDoesNotAutoCloseRoot_9y2f3(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)
	p := bdProxiedInit(t, bd, "pun")

	root := bdProxiedCreate(t, bd, p.dir, "molecule root", "--type", "molecule")
	step1 := bdProxiedCreate(t, bd, p.dir, "step 1", "--type", "task")
	step2 := bdProxiedCreate(t, bd, p.dir, "step 2", "--type", "task")
	for _, stepID := range []string{step1.ID, step2.ID} {
		if out, err := bdProxiedRun(t, bd, p.dir, "dep", "add", stepID, root.ID, "--type", "parent-child"); err != nil {
			t.Fatalf("proxied dep add %s -> %s failed: %v\n%s", stepID, root.ID, err, out)
		}
	}

	if out, err := bdProxiedRun(t, bd, p.dir, "update", step1.ID, "--status", "closed"); err != nil {
		t.Fatalf("proxied `bd update --status closed` of a non-final step failed: %v\n%s", err, out)
	}

	if root := bdProxiedShow(t, bd, p.dir, root.ID); root.Status == types.StatusClosed {
		t.Errorf("molecule root %s auto-closed after closing only ONE of two steps — the cascade must fire only on real completion (beads-9y2f3)", root.ID)
	}
}
