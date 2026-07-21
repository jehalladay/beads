//go:build cgo

package main

import (
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// beads-r3m8v (PROXIED legs): the proxied `bd supersede`/`bd duplicate` legs run
// through runLinkAndCloseProxied, which closes the source via CloseIssue/CloseWisp
// on the UOW but — like the direct legs (duplicate.go) — dropped the
// GC-survivable audit-FILE trail (.beads/interactions.jsonl) that `bd close`/`bd
// update` write via auditStatusChange (beads-n4sn). So on a hub-connected
// sql-server crew, a superseded/duplicated issue's close vanished from the
// durable record after a Dolt GC flatten.
//
// These are the 2 PROXIED legs completing r3m8v's "all 4 legs" scope (the direct
// legs are covered by the embedded sibling). The fix emits auditStatusChange
// AFTER uw.Commit succeeds (a pre-commit emit would orphan the cwd-file entry if
// the deferred uw.Close rolled the tx back — matching the batch c2pr1 flush).
// End-to-end through the real proxied `bd` subprocess (BEADS_TEST_PROXIED_SERVER=1).
// MUTATION-VERIFIED: removing the auditStatusChange call in runLinkAndCloseProxied
// drops the entry → these parity assertions go RED.

func TestProxiedSupersede_WritesGCSurvivableAuditTrail_r3m8v(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)
	p := bdProxiedInit(t, bd, "psa2")

	// CONTROL: a single-path proxied close writes the status audit trail.
	ctrl := bdProxiedCreate(t, bd, p.dir, "control close", "--type", "task")
	if out, err := bdProxiedRun(t, bd, p.dir, "close", ctrl.ID, "--reason", "control"); err != nil {
		t.Fatalf("CONTROL proxied close %s failed: %v\n%s", ctrl.ID, err, out)
	}
	if !auditHasStatusChange(t, p.dir, ctrl.ID, "closed") {
		t.Fatalf("CONTROL: proxied single-path close did not write a status field_change for %s — harness broken", ctrl.ID)
	}

	// TEST: proxied supersede must write the SAME GC-survivable audit trail.
	old := bdProxiedCreate(t, bd, p.dir, "to be superseded", "--type", "task")
	replacement := bdProxiedCreate(t, bd, p.dir, "replacement", "--type", "task")
	if out, err := bdProxiedRun(t, bd, p.dir, "supersede", old.ID, "--with", replacement.ID); err != nil {
		t.Fatalf("proxied `bd supersede` failed: %v\n%s", err, out)
	}
	if bdProxiedShow(t, bd, p.dir, old.ID).Status != types.StatusClosed {
		t.Fatalf("proxied `bd supersede` did not close %s", old.ID)
	}
	if !auditHasStatusChange(t, p.dir, old.ID, "closed") {
		t.Errorf("proxied `bd supersede` did not write a GC-survivable audit field_change to status=closed for %s (beads-r3m8v) — parity with single-path close broken", old.ID)
	}
}

func TestProxiedDuplicate_WritesGCSurvivableAuditTrail_r3m8v(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)
	p := bdProxiedInit(t, bd, "pda2")

	// CONTROL baseline.
	ctrl := bdProxiedCreate(t, bd, p.dir, "control close", "--type", "task")
	if out, err := bdProxiedRun(t, bd, p.dir, "close", ctrl.ID, "--reason", "control"); err != nil {
		t.Fatalf("CONTROL proxied close %s failed: %v\n%s", ctrl.ID, err, out)
	}
	if !auditHasStatusChange(t, p.dir, ctrl.ID, "closed") {
		t.Fatalf("CONTROL: proxied single-path close did not write a status field_change for %s — harness broken", ctrl.ID)
	}

	// TEST: proxied duplicate must write the SAME GC-survivable audit trail.
	dup := bdProxiedCreate(t, bd, p.dir, "to be duplicated", "--type", "task")
	canonical := bdProxiedCreate(t, bd, p.dir, "canonical", "--type", "task")
	if out, err := bdProxiedRun(t, bd, p.dir, "duplicate", dup.ID, "--of", canonical.ID); err != nil {
		t.Fatalf("proxied `bd duplicate` failed: %v\n%s", err, out)
	}
	if bdProxiedShow(t, bd, p.dir, dup.ID).Status != types.StatusClosed {
		t.Fatalf("proxied `bd duplicate` did not close %s", dup.ID)
	}
	if !auditHasStatusChange(t, p.dir, dup.ID, "closed") {
		t.Errorf("proxied `bd duplicate` did not write a GC-survivable audit field_change to status=closed for %s (beads-r3m8v) — parity with single-path close broken", dup.ID)
	}
}
