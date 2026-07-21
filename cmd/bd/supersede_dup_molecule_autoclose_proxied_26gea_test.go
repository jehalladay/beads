//go:build cgo

package main

import (
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// beads-26gea (PROXIED legs): the proxied `bd supersede`/`bd duplicate` legs run
// through runLinkAndCloseProxied, which closes the source via CloseIssue but,
// like the direct legs (duplicate.go) and `bd update --status closed` (zzp26),
// bypassed the completed-molecule auto-close cascade `bd close` runs. So on a
// hub-connected sql-server crew, superseding/duplicating a molecule's FINAL step
// left the auto-closing root stuck OPEN. These are the 2 PROXIED legs completing
// 26gea's "all 4 legs" scope (the direct legs are covered by the embedded
// sibling test). Runs end-to-end through the real `bd` proxied-server subprocess
// (BEADS_TEST_PROXIED_SERVER=1). MUTATION-VERIFIED: removing the
// autoCloseProxiedCompletedMolecule call in runLinkAndCloseProxied (or moving it
// after uw.Commit so its staged root-close is rolled back) leaves the root OPEN.

// seedProxiedMoleculeLastStepOpen builds, via the real proxied `bd`, a molecule
// root with two parent-child steps, step1 pre-closed, step2 open. Returns the
// root id and the still-open last-step id.
func seedProxiedMoleculeLastStepOpen(t *testing.T, bd string, p proxiedProject) (rootID, lastStepID string) {
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

func TestProxiedSupersede_AutoClosesCompletedMolecule_26gea(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)
	p := bdProxiedInit(t, bd, "psa")

	rootID, lastStep := seedProxiedMoleculeLastStepOpen(t, bd, p)
	replacement := bdProxiedCreate(t, bd, p.dir, "replacement", "--type", "task")

	if out, err := bdProxiedRun(t, bd, p.dir, "supersede", lastStep, "--with", replacement.ID); err != nil {
		t.Fatalf("proxied `bd supersede` of last step failed: %v\n%s", err, out)
	}

	if root := bdProxiedShow(t, bd, p.dir, rootID); root.Status != types.StatusClosed {
		t.Errorf("molecule root %s status = %q, want %q — proxied `bd supersede` of the final step did not auto-close the completed molecule (beads-26gea)", rootID, root.Status, types.StatusClosed)
	}
}

func TestProxiedDuplicate_AutoClosesCompletedMolecule_26gea(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)
	p := bdProxiedInit(t, bd, "pda")

	rootID, lastStep := seedProxiedMoleculeLastStepOpen(t, bd, p)
	canonical := bdProxiedCreate(t, bd, p.dir, "canonical", "--type", "task")

	if out, err := bdProxiedRun(t, bd, p.dir, "duplicate", lastStep, "--of", canonical.ID); err != nil {
		t.Fatalf("proxied `bd duplicate` of last step failed: %v\n%s", err, out)
	}

	if root := bdProxiedShow(t, bd, p.dir, rootID); root.Status != types.StatusClosed {
		t.Errorf("molecule root %s status = %q, want %q — proxied `bd duplicate` of the final step did not auto-close the completed molecule (beads-26gea)", rootID, root.Status, types.StatusClosed)
	}
}

// Negative (no false positive): proxied supersede of a NON-final step must NOT
// auto-close the root.
func TestProxiedSupersede_NonFinalStepDoesNotAutoCloseRoot_26gea(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)
	p := bdProxiedInit(t, bd, "psn")

	root := bdProxiedCreate(t, bd, p.dir, "molecule root", "--type", "molecule")
	step1 := bdProxiedCreate(t, bd, p.dir, "step 1", "--type", "task")
	step2 := bdProxiedCreate(t, bd, p.dir, "step 2", "--type", "task")
	for _, stepID := range []string{step1.ID, step2.ID} {
		if out, err := bdProxiedRun(t, bd, p.dir, "dep", "add", stepID, root.ID, "--type", "parent-child"); err != nil {
			t.Fatalf("proxied dep add %s -> %s failed: %v\n%s", stepID, root.ID, err, out)
		}
	}
	replacement := bdProxiedCreate(t, bd, p.dir, "replacement", "--type", "task")

	if out, err := bdProxiedRun(t, bd, p.dir, "supersede", step1.ID, "--with", replacement.ID); err != nil {
		t.Fatalf("proxied `bd supersede` of a non-final step failed: %v\n%s", err, out)
	}

	if root := bdProxiedShow(t, bd, p.dir, root.ID); root.Status == types.StatusClosed {
		t.Errorf("molecule root %s auto-closed after superseding only ONE of two steps — the cascade must fire only on real completion (beads-26gea)", root.ID)
	}
}
