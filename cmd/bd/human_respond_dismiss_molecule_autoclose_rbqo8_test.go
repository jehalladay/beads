//go:build cgo

package main

import (
	"os/exec"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// beads-rbqo8 (CLOSE-PARITY-MATRIX cascade legs): closing a human-gate bead that
// is a molecule's FINAL open step via `bd human respond` / `bd human dismiss`
// closed the step (CloseIssue) but — unlike `bd close` (close.go:223), `bd todo
// done` (58kg8), and supersede/duplicate (26gea) — never fired the auto-close
// cascade. So a molecule whose last step is a human-gate bead was stranded OPEN
// after the operator resolved that gate the normal way: every step closed, root
// never closed. The fix calls autoCloseCompletedMolecule (direct legs, human.go)
// / autoCloseProxiedCompletedMolecule staged into the UOW before commit (proxied
// legs, human_proxied_server.go) after the successful CloseIssue on all 4 legs.
//
// The audit-file trail is already at parity (mw44m) — this is a PURE cascade gap.
// These run end-to-end through the real `bd` subprocess (the cascade + its
// cwd-based audit are only exercised by the actual command handlers). The direct
// legs use the embedded backend; the proxied legs run against a real proxied
// sql-server (BEADS_TEST_PROXIED_SERVER=1), mirroring the mw44m/26gea siblings.
//
// MUTATION-VERIFIED: removing the autoCloseCompletedMolecule call in the human.go
// respond/dismiss legs → the direct root-open assertions go RED (root stays open,
// "Successfully"-closed step, molecule stuck); removing / mis-ordering the
// autoCloseProxiedCompletedMolecule call in the proxied legs → the proxied
// assertions go RED.

// seedMoleculeLastStepIsHumanGate builds a molecule root with two parent-child
// steps via the real embedded `bd`: step1 pre-closed, step2 still OPEN and
// labeled "human" (a human-gate bead). Returns the root id and the open
// human-gate last-step id.
func seedMoleculeLastStepIsHumanGate(t *testing.T, bd, dir string) (rootID, humanStepID string) {
	t.Helper()
	rootID, lastStepID := seedMoleculeLastStepOpen(t, bd, dir)
	labelCmd := exec.Command(bd, "label", "add", lastStepID, "human")
	labelCmd.Dir = dir
	labelCmd.Env = bdEnv(dir)
	if out, err := labelCmd.CombinedOutput(); err != nil {
		t.Fatalf("label add human %s failed: %v\n%s", lastStepID, err, out)
	}
	return rootID, lastStepID
}

func TestHumanRespond_AutoClosesCompletedMolecule_rbqo8(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "hrm")

	rootID, humanStep := seedMoleculeLastStepIsHumanGate(t, bd, dir)

	cmd := exec.Command(bd, "human", "respond", humanStep, "-r", "here is the answer")
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	stdout, stderr, err := runCommandBuffers(t, cmd)
	if err != nil {
		t.Fatalf("`bd human respond` of the final human-gate step failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	if bdShow(t, bd, dir, humanStep).Status != types.StatusClosed {
		t.Fatalf("`bd human respond` did not close the human-gate step %s", humanStep)
	}

	root := bdShow(t, bd, dir, rootID)
	if root.Status != types.StatusClosed {
		t.Errorf("molecule root %s status = %q, want %q — `bd human respond` of the final human-gate step did not auto-close the completed molecule (beads-rbqo8)\nrespond stdout:\n%s", rootID, root.Status, types.StatusClosed, stdout.String())
	}
}

func TestHumanDismiss_AutoClosesCompletedMolecule_rbqo8(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "hdm")

	rootID, humanStep := seedMoleculeLastStepIsHumanGate(t, bd, dir)

	cmd := exec.Command(bd, "human", "dismiss", humanStep, "--reason", "no longer applicable")
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	stdout, stderr, err := runCommandBuffers(t, cmd)
	if err != nil {
		t.Fatalf("`bd human dismiss` of the final human-gate step failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	if bdShow(t, bd, dir, humanStep).Status != types.StatusClosed {
		t.Fatalf("`bd human dismiss` did not close the human-gate step %s", humanStep)
	}

	root := bdShow(t, bd, dir, rootID)
	if root.Status != types.StatusClosed {
		t.Errorf("molecule root %s status = %q, want %q — `bd human dismiss` of the final human-gate step did not auto-close the completed molecule (beads-rbqo8)\ndismiss stdout:\n%s", rootID, root.Status, types.StatusClosed, stdout.String())
	}
}

// Negative (no false positive): responding to a NON-final human-gate step must
// NOT auto-close the root — a sibling step is still open.
func TestHumanRespond_NonFinalStepDoesNotAutoCloseRoot_rbqo8(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "hrn")

	root := bdCreate(t, bd, dir, "molecule root", "--type", "molecule")
	step1 := bdCreate(t, bd, dir, "step 1", "--type", "task", "--labels", "human")
	step2 := bdCreate(t, bd, dir, "step 2", "--type", "task")
	for _, stepID := range []string{step1.ID, step2.ID} {
		depCmd := exec.Command(bd, "dep", "add", stepID, root.ID, "--type", "parent-child")
		depCmd.Dir = dir
		depCmd.Env = bdEnv(dir)
		if out, err := depCmd.CombinedOutput(); err != nil {
			t.Fatalf("dep add %s -> %s failed: %v\n%s", stepID, root.ID, err, out)
		}
	}

	cmd := exec.Command(bd, "human", "respond", step1.ID, "-r", "answered")
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	if stdout, stderr, err := runCommandBuffers(t, cmd); err != nil {
		t.Fatalf("`bd human respond` of a non-final step failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}

	if got := bdShow(t, bd, dir, root.ID).Status; got == types.StatusClosed {
		t.Errorf("molecule root %s was auto-closed by responding to a NON-final step (step2 still open) — false-positive cascade (beads-rbqo8)", root.ID)
	}
}

// seedProxiedMoleculeLastStepIsHumanGate: proxied analogue of the embedded seed,
// with the still-open last step labeled "human".
func seedProxiedMoleculeLastStepIsHumanGate(t *testing.T, bd string, p proxiedProject) (rootID, humanStepID string) {
	t.Helper()
	rootID, lastStepID := seedProxiedMoleculeLastStepOpen(t, bd, p)
	if out, err := bdProxiedRun(t, bd, p.dir, "label", "add", lastStepID, "human"); err != nil {
		t.Fatalf("proxied label add human %s failed: %v\n%s", lastStepID, err, out)
	}
	return rootID, lastStepID
}

func TestProxiedHumanRespond_AutoClosesCompletedMolecule_rbqo8(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)
	p := bdProxiedInit(t, bd, "phrm")

	rootID, humanStep := seedProxiedMoleculeLastStepIsHumanGate(t, bd, p)

	if out, err := bdProxiedRun(t, bd, p.dir, "human", "respond", humanStep, "-r", "here is the answer"); err != nil {
		t.Fatalf("proxied `bd human respond` of the final human-gate step failed: %v\n%s", err, out)
	}
	if bdProxiedShow(t, bd, p.dir, humanStep).Status != types.StatusClosed {
		t.Fatalf("proxied `bd human respond` did not close the human-gate step %s", humanStep)
	}

	if root := bdProxiedShow(t, bd, p.dir, rootID); root.Status != types.StatusClosed {
		t.Errorf("molecule root %s status = %q, want %q — proxied `bd human respond` of the final human-gate step did not auto-close the completed molecule (beads-rbqo8)", rootID, root.Status, types.StatusClosed)
	}
}

func TestProxiedHumanDismiss_AutoClosesCompletedMolecule_rbqo8(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)
	p := bdProxiedInit(t, bd, "phdm")

	rootID, humanStep := seedProxiedMoleculeLastStepIsHumanGate(t, bd, p)

	if out, err := bdProxiedRun(t, bd, p.dir, "human", "dismiss", humanStep, "--reason", "no longer applicable"); err != nil {
		t.Fatalf("proxied `bd human dismiss` of the final human-gate step failed: %v\n%s", err, out)
	}
	if bdProxiedShow(t, bd, p.dir, humanStep).Status != types.StatusClosed {
		t.Fatalf("proxied `bd human dismiss` did not close the human-gate step %s", humanStep)
	}

	if root := bdProxiedShow(t, bd, p.dir, rootID); root.Status != types.StatusClosed {
		t.Errorf("molecule root %s status = %q, want %q — proxied `bd human dismiss` of the final human-gate step did not auto-close the completed molecule (beads-rbqo8)", rootID, root.Status, types.StatusClosed)
	}
}
