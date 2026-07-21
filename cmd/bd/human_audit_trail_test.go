//go:build cgo

package main

import (
	"os/exec"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// beads-mw44m: `bd human respond` / `bd human dismiss` close the human-gate bead
// via store.CloseIssue (direct) / uw.IssueUseCase().CloseIssue|CloseWisp
// (proxied), which records the DB EventClosed row but did NOT write the
// GC-survivable audit-FILE trail (.beads/interactions.jsonl) that bd close/
// update/gate-resolve/supersede/duplicate emit via auditStatusChange
// (beads-n4sn / beads-r3m8v / beads-1jkl5). The audit trail exists specifically
// because it survives a Dolt GC flatten (which destroys commit history) — so
// after a flatten a responded/dismissed bead's close vanished from the durable
// record while a plainly-closed issue's did not.
//
// These are the DIRECT legs (the proxied legs are covered by the *_proxied_*
// sibling under BEADS_TEST_PROXIED_SERVER=1). audit-file-parity sibling of
// r3m8v/1jkl5 on the human-gate close verbs. End-to-end through the real `bd`
// subprocess (the tx-helper path can't exercise the cwd-based audit FILE — only
// the real cmd handler writes it, c2pr1/r3m8v lesson).
// MUTATION-VERIFIED: removing the auditStatusChange call in the respective
// human.go leg drops the entry → the parity assertion goes RED.

func TestHumanRespond_WritesGCSurvivableAuditTrail_mw44m(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "hra")

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

	// TEST: responding to a human bead must write the SAME GC-survivable trail.
	hb := bdCreate(t, bd, dir, "needs a human", "--type", "task", "--labels", "human")
	rc := exec.Command(bd, "human", "respond", hb.ID, "-r", "here is the answer")
	rc.Dir = dir
	rc.Env = bdEnv(dir)
	if stdout, stderr, err := runCommandBuffers(t, rc); err != nil {
		t.Fatalf("`bd human respond` failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	if bdShow(t, bd, dir, hb.ID).Status != types.StatusClosed {
		t.Fatalf("`bd human respond` did not close %s", hb.ID)
	}
	if !auditHasStatusChange(t, dir, hb.ID, "closed") {
		t.Errorf("`bd human respond` did not write a GC-survivable audit field_change to status=closed for %s (beads-mw44m) — parity with single-path close broken", hb.ID)
	}
}

func TestHumanDismiss_WritesGCSurvivableAuditTrail_mw44m(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "hda")

	// CONTROL baseline.
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

	// TEST: dismissing a human bead must write the SAME GC-survivable trail.
	hb := bdCreate(t, bd, dir, "needs a human", "--type", "task", "--labels", "human")
	dc := exec.Command(bd, "human", "dismiss", hb.ID, "--reason", "no longer applicable")
	dc.Dir = dir
	dc.Env = bdEnv(dir)
	if stdout, stderr, err := runCommandBuffers(t, dc); err != nil {
		t.Fatalf("`bd human dismiss` failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	if bdShow(t, bd, dir, hb.ID).Status != types.StatusClosed {
		t.Fatalf("`bd human dismiss` did not close %s", hb.ID)
	}
	if !auditHasStatusChange(t, dir, hb.ID, "closed") {
		t.Errorf("`bd human dismiss` did not write a GC-survivable audit field_change to status=closed for %s (beads-mw44m) — parity with single-path close broken", hb.ID)
	}
}
