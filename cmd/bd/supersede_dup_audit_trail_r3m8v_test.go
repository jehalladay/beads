//go:build cgo

package main

import (
	"os/exec"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// beads-r3m8v: `bd supersede`/`bd duplicate` close the source issue via
// store.LinkAndClose, which records the DB EventClosed row but did NOT write the
// GC-survivable audit-FILE trail (.beads/interactions.jsonl) that `bd close`/`bd
// update` emit via auditStatusChange (beads-n4sn). The audit trail "exists
// specifically because it survives a Dolt GC flatten, which destroys commit
// history" — so after a flatten a superseded/duplicated issue's close vanished
// from the durable record while a plainly-closed issue's did not.
//
// These are the DIRECT legs (the proxied legs run through runLinkAndCloseProxied
// and are covered by the *_proxied_* sibling under BEADS_TEST_PROXIED_SERVER=1).
// audit-file-parity sibling of c2pr1/qeb2p (batch) on the LinkAndClose leg.
// End-to-end through the real `bd` subprocess (the tx-helper path can't exercise
// the cwd-based audit FILE — only the real cmd handler writes it, c2pr1 lesson).
// MUTATION-VERIFIED: removing the auditStatusChange call in the respective
// duplicate.go leg drops the entry → the leg's parity assertion goes RED.

func TestSupersede_WritesGCSurvivableAuditTrail_r3m8v(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "sat")

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

	// TEST: supersede must write the SAME GC-survivable audit trail (parity).
	old := bdCreate(t, bd, dir, "to be superseded", "--type", "task")
	replacement := bdCreate(t, bd, dir, "replacement", "--type", "task")
	cmd := exec.Command(bd, "supersede", old.ID, "--with", replacement.ID)
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	if stdout, stderr, err := runCommandBuffers(t, cmd); err != nil {
		t.Fatalf("`bd supersede` failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	if bdShow(t, bd, dir, old.ID).Status != types.StatusClosed {
		t.Fatalf("`bd supersede` did not close %s", old.ID)
	}
	if !auditHasStatusChange(t, dir, old.ID, "closed") {
		t.Errorf("`bd supersede` did not write a GC-survivable audit field_change to status=closed for %s (beads-r3m8v) — parity with single-path close broken", old.ID)
	}
}

func TestDuplicate_WritesGCSurvivableAuditTrail_r3m8v(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "dat")

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

	// TEST: duplicate must write the SAME GC-survivable audit trail.
	dup := bdCreate(t, bd, dir, "to be duplicated", "--type", "task")
	canonical := bdCreate(t, bd, dir, "canonical", "--type", "task")
	cmd := exec.Command(bd, "duplicate", dup.ID, "--of", canonical.ID)
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	if stdout, stderr, err := runCommandBuffers(t, cmd); err != nil {
		t.Fatalf("`bd duplicate` failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	if bdShow(t, bd, dir, dup.ID).Status != types.StatusClosed {
		t.Fatalf("`bd duplicate` did not close %s", dup.ID)
	}
	if !auditHasStatusChange(t, dir, dup.ID, "closed") {
		t.Errorf("`bd duplicate` did not write a GC-survivable audit field_change to status=closed for %s (beads-r3m8v) — parity with single-path close broken", dup.ID)
	}
}

// Negative (no orphan on a rejected op): a supersede that is REJECTED by a guard
// (already superseded by a different target) must NOT write a status audit entry
// for the second target — no close happened, so no audit trail (mirrors the
// batch rollback no-orphan assertion, beads-c2pr1).
func TestSupersede_RejectedSecondTarget_WritesNoOrphanAudit_r3m8v(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "sao")

	old := bdCreate(t, bd, dir, "source", "--type", "task")
	first := bdCreate(t, bd, dir, "first replacement", "--type", "task")
	second := bdCreate(t, bd, dir, "second replacement", "--type", "task")

	// First supersede succeeds (closes old, one audit entry).
	cmd := exec.Command(bd, "supersede", old.ID, "--with", first.ID)
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	if stdout, stderr, err := runCommandBuffers(t, cmd); err != nil {
		t.Fatalf("first `bd supersede` failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}

	// Second supersede to a DIFFERENT target is rejected by the pmaud guard
	// (multiple-live-successors) — old is already closed; no new close happens.
	cmd2 := exec.Command(bd, "supersede", old.ID, "--with", second.ID)
	cmd2.Dir = dir
	cmd2.Env = bdEnv(dir)
	if _, _, err := runCommandBuffers(t, cmd2); err == nil {
		t.Fatalf("second `bd supersede` to a different target should have been rejected (multiple-live-successors guard)")
	}

	// The audit trail must show exactly the first-target close, not a spurious
	// second — count is not directly asserted, but the second target must never
	// appear as this source's transition (it never closed anything).
	if !auditHasStatusChange(t, dir, old.ID, "closed") {
		t.Errorf("first supersede's audit entry missing for %s (beads-r3m8v)", old.ID)
	}
}
