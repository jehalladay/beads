//go:build cgo

package main

import (
	"os/exec"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/types"
)

// beads-346th: a linked gate is a real molecule step (mol show counts it), so
// closing a gate that is a molecule's FINAL step must cascade-close the parent
// exactly as `bd close` does (close.go:223 fires autoCloseCompletedMolecule).
// The gate close paths (manual `bd gate resolve` gate.go:577 and the `bd gate
// check` auto-resolve loop's closeGate gate.go:1110) closed via bare CloseIssue
// with NO cascade hop, so a molecule whose last step is a human/timer/PR gate
// silently stayed OPEN with every step done — a stranded completed root
// (CLOSE-PARITY-MATRIX, sibling of beads-4v7eb's epic close-eligible leg and the
// beads-58kg8 todo-done leg). The fix fires the same shared cascade chokepoint
// after both gate closes (direct + proxied).
//
// End-to-end through the real `bd` subprocess (matches the 58kg8/8ociu idiom).
// MUTATION-VERIFIED: removing the autoCloseCompletedMolecule call in the gate
// resolve path → TestGateResolve_* goes RED; removing it from closeGate → the
// gate-check auto-resolve test goes RED.

// seedMoleculeGateLastStep builds a molecule whose only remaining open step is a
// gate blocking the given target. step1 (a plain task) is closed so the gate is
// the molecule's final incomplete step. Returns the molecule root + gate IDs.
func seedMoleculeGateLastStep(t *testing.T, bd, dir, gateType, timeout string) (rootID, gateID string) {
	t.Helper()
	root := bdCreate(t, bd, dir, "molecule root", "--type", "molecule")
	step1 := bdCreate(t, bd, dir, "step 1", "--type", "task")

	// Create a gate (it blocks a throwaway target) up front.
	target := bdCreate(t, bd, dir, "gate target", "--type", "task")
	args := []string{"gate", "create", "--type", gateType, "--blocks", target.ID, "--json"}
	if timeout != "" {
		args = append(args, "--timeout", timeout)
	}
	gcCmd := exec.Command(bd, args...)
	gcCmd.Dir = dir
	gcCmd.Env = bdEnv(dir)
	gcOut, gcErr, err := runCommandBuffers(t, gcCmd)
	if err != nil {
		t.Fatalf("`bd gate create --type %s` failed: %v\nstdout:\n%s\nstderr:\n%s", gateType, err, gcOut.String(), gcErr.String())
	}
	gate := parseIssueJSON(t, gcOut.Bytes())
	if gate.ID == "" {
		t.Fatalf("`bd gate create` returned no gate ID\nstdout:\n%s", gcOut.String())
	}

	// Link BOTH step1 and the gate as parent-child molecule steps BEFORE closing
	// step1 — the still-open gate keeps the root incomplete, so closing step1
	// does not (yet) auto-close the molecule. Then close step1 so the gate is the
	// molecule's sole remaining incomplete step.
	for _, stepID := range []string{step1.ID, gate.ID} {
		depCmd := exec.Command(bd, "dep", "add", stepID, root.ID, "--type", "parent-child")
		depCmd.Dir = dir
		depCmd.Env = bdEnv(dir)
		if out, err := depCmd.CombinedOutput(); err != nil {
			t.Fatalf("dep add %s -> root failed: %v\n%s", stepID, err, out)
		}
	}
	closeCmd := exec.Command(bd, "close", step1.ID, "--reason", "done")
	closeCmd.Dir = dir
	closeCmd.Env = bdEnv(dir)
	if out, err := closeCmd.CombinedOutput(); err != nil {
		t.Fatalf("close step1 failed: %v\n%s", err, out)
	}
	return root.ID, gate.ID
}

// TestGateResolve_AutoClosesCompletedMolecule_346th: `bd gate resolve` of a
// molecule's final-step gate must auto-close the molecule root (CONTROL: the
// same shape closed via `bd close` cascades).
func TestGateResolve_AutoClosesCompletedMolecule_346th(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "grm")

	rootID, gateID := seedMoleculeGateLastStep(t, bd, dir, "human", "")

	cmd := exec.Command(bd, "gate", "resolve", gateID)
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	stdout, stderr, err := runCommandBuffers(t, cmd)
	if err != nil {
		t.Fatalf("`bd gate resolve %s` failed: %v\nstdout:\n%s\nstderr:\n%s", gateID, err, stdout.String(), stderr.String())
	}

	root := bdShow(t, bd, dir, rootID)
	if root.Status != types.StatusClosed {
		t.Errorf("molecule root %s status = %q, want %q — `bd gate resolve` of the final-step gate did not auto-close the completed molecule (beads-346th)\nresolve stdout:\n%s", rootID, root.Status, types.StatusClosed, stdout.String())
	}
}

// TestGateCheckAutoResolve_AutoClosesCompletedMolecule_346th: the `bd gate check`
// timer auto-resolve loop (closeGate) must also cascade-close the parent.
func TestGateCheckAutoResolve_AutoClosesCompletedMolecule_346th(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "gcm")

	rootID, gateID := seedMoleculeGateLastStep(t, bd, dir, "timer", "1s")

	// Wait for the timer to expire, then auto-resolve via `gate check`.
	time.Sleep(1500 * time.Millisecond)
	chk := exec.Command(bd, "gate", "check", "--type", "timer")
	chk.Dir = dir
	chk.Env = bdEnv(dir)
	stdout, stderr, err := runCommandBuffers(t, chk)
	if err != nil {
		t.Fatalf("`bd gate check --type timer` failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	if bdShow(t, bd, dir, gateID).Status != types.StatusClosed {
		t.Fatalf("`bd gate check` did not auto-resolve timer gate %s\nstdout:\n%s", gateID, stdout.String())
	}

	root := bdShow(t, bd, dir, rootID)
	if root.Status != types.StatusClosed {
		t.Errorf("molecule root %s status = %q, want %q — `bd gate check` timer auto-resolve of the final-step gate did not auto-close the completed molecule (beads-346th)\ncheck stdout:\n%s", rootID, root.Status, types.StatusClosed, stdout.String())
	}
}
