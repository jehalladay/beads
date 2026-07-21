//go:build cgo

package main

import (
	"os/exec"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// beads-1jkl5: `bd gate resolve` closes the gate issue via store.CloseIssue
// (direct) / uw.IssueUseCase().CloseIssue (proxied), which records the DB
// EventClosed row but did NOT write the GC-survivable audit-FILE trail
// (.beads/interactions.jsonl) that `bd close`/`bd update`/`bd supersede`/`bd
// duplicate` emit via auditStatusChange (beads-n4sn / beads-r3m8v). The audit
// trail exists specifically because it survives a Dolt GC flatten (which
// destroys commit history) — so after a flatten a RESOLVED gate's close
// vanished from the durable record while a plainly-closed issue's did not.
//
// This is the direct leg (the proxied leg is covered by the *_proxied_* sibling
// under BEADS_TEST_PROXIED_SERVER=1). audit-file-parity sibling of r3m8v
// (supersede/duplicate LinkAndClose) and c2pr1/qeb2p (batch) — the same n4sn
// class on the gate-resolve close leg. End-to-end through the real `bd`
// subprocess (the tx-helper path can't exercise the cwd-based audit FILE — only
// the real cmd handler writes it, c2pr1/r3m8v lesson).
// MUTATION-VERIFIED: removing the auditStatusChange call in gate.go's resolve
// leg drops the entry → the parity assertion goes RED.

func TestGateResolve_WritesGCSurvivableAuditTrail(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "grt")

	// CONTROL: a single-path close writes the status audit trail (baseline —
	// proves the harness detects the entry a plain close emits).
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

	// TEST: resolving a gate must write the SAME GC-survivable audit trail.
	target := bdCreate(t, bd, dir, "gate target", "--type", "task")
	// `bd gate create --json` prints the gate issue object; capture its ID.
	gcCmd := exec.Command(bd, "gate", "create", "--blocks", target.ID, "--json")
	gcCmd.Dir = dir
	gcCmd.Env = bdEnv(dir)
	gcOut, gcErr, err := runCommandBuffers(t, gcCmd)
	if err != nil {
		t.Fatalf("`bd gate create` failed: %v\nstdout:\n%s\nstderr:\n%s", err, gcOut.String(), gcErr.String())
	}
	gateIssue := parseIssueJSON(t, gcOut.Bytes())
	if gateIssue.ID == "" {
		t.Fatalf("`bd gate create` did not return a gate ID\nstdout:\n%s", gcOut.String())
	}

	grCmd := exec.Command(bd, "gate", "resolve", gateIssue.ID, "-r", "done")
	grCmd.Dir = dir
	grCmd.Env = bdEnv(dir)
	if stdout, stderr, err := runCommandBuffers(t, grCmd); err != nil {
		t.Fatalf("`bd gate resolve` failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	if bdShow(t, bd, dir, gateIssue.ID).Status != types.StatusClosed {
		t.Fatalf("`bd gate resolve` did not close gate %s", gateIssue.ID)
	}
	if !auditHasStatusChange(t, dir, gateIssue.ID, "closed") {
		t.Errorf("`bd gate resolve` did not write a GC-survivable audit field_change to status=closed for gate %s — parity with single-path close broken", gateIssue.ID)
	}
}
