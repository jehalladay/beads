//go:build cgo

package main

import (
	"os/exec"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/types"
)

// beads-8ociu: the `bd gate check` AUTO-RESOLVE path (timer / gh:run / gh:pr →
// closeGate / closeGateProxied) closes the gate via CloseIssue but did NOT write
// the GC-survivable audit-FILE trail (.beads/interactions.jsonl) that MANUAL
// `bd gate resolve` writes via auditStatusChange (beads-1jkl5), and that bd
// close/supersede emit (beads-n4sn / beads-r3m8v). So after a Dolt GC flatten an
// auto-resolved gate's close vanished from the durable record while a manually
// resolved one's did not. Empirically reproduced by beads_dogfooder (SWEEP-239):
// a 1s timer gate auto-resolved via `gate check --type timer` has status=closed
// but NO field_change in interactions.jsonl.
//
// This is the DIRECT leg (the proxied leg is covered by the *_proxied_* sibling
// under BEADS_TEST_PROXIED_SERVER=1). auto-resolve sibling of 1jkl5. Timer is the
// deterministic auto-resolve type (gh:run/gh:pr need an external service); the
// closeGate/closeGateProxied fix covers all three. End-to-end through the real
// `bd` subprocess (only the real cmd handler writes the cwd audit FILE).
// MUTATION-VERIFIED: removing the auditStatusChange call in closeGate drops the
// entry → the parity assertion goes RED.

func TestGateCheckAutoResolve_WritesGCSurvivableAuditTrail_8ociu(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "gar")

	// CONTROL: a single-path close writes the status audit trail.
	ctrl := bdCreate(t, bd, dir, "control close", "--type", "task")
	if cmd := exec.Command(bd, "close", ctrl.ID, "-r", "control"); true {
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		if _, stderr, err := runCommandBuffers(t, cmd); err != nil {
			t.Fatalf("CONTROL bd close %s failed: %v\nstderr:\n%s", ctrl.ID, err, stderr.String())
		}
	}
	if !auditHasStatusChange(t, dir, ctrl.ID, "closed") {
		t.Fatalf("CONTROL: single-path close did not write a status field_change for %s — harness broken", ctrl.ID)
	}

	// Create a timer gate that expires almost immediately, blocking a target.
	target := bdCreate(t, bd, dir, "gate target", "--type", "task")
	gcCmd := exec.Command(bd, "gate", "create", "--type", "timer", "--timeout", "1s", "--blocks", target.ID, "--json")
	gcCmd.Dir = dir
	gcCmd.Env = bdEnv(dir)
	gcOut, gcErr, err := runCommandBuffers(t, gcCmd)
	if err != nil {
		t.Fatalf("`bd gate create --type timer` failed: %v\nstdout:\n%s\nstderr:\n%s", err, gcOut.String(), gcErr.String())
	}
	gateIssue := parseIssueJSON(t, gcOut.Bytes())
	if gateIssue.ID == "" {
		t.Fatalf("`bd gate create` did not return a gate ID\nstdout:\n%s", gcOut.String())
	}

	// Wait for the timer to expire, then auto-resolve via `gate check`.
	time.Sleep(1500 * time.Millisecond)
	chkCmd := exec.Command(bd, "gate", "check", "--type", "timer")
	chkCmd.Dir = dir
	chkCmd.Env = bdEnv(dir)
	if stdout, stderr, err := runCommandBuffers(t, chkCmd); err != nil {
		t.Fatalf("`bd gate check --type timer` failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	if bdShow(t, bd, dir, gateIssue.ID).Status != types.StatusClosed {
		t.Fatalf("`bd gate check` did not auto-resolve (close) timer gate %s", gateIssue.ID)
	}
	if !auditHasStatusChange(t, dir, gateIssue.ID, "closed") {
		t.Errorf("`bd gate check` auto-resolve did not write a GC-survivable audit field_change to status=closed for gate %s (beads-8ociu) — parity with manual resolve broken", gateIssue.ID)
	}
}
