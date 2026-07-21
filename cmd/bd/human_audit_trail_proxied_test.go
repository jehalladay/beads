//go:build cgo

package main

import (
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// beads-mw44m (PROXIED legs): the proxied `bd human respond` / `bd human dismiss`
// run through runHumanRespondProxiedServer / runHumanDismissProxiedServer, which
// close the bead via uw.IssueUseCase().CloseIssue|CloseWisp but — like the direct
// legs (human.go) — dropped the GC-survivable audit-FILE trail
// (.beads/interactions.jsonl) that bd close/gate-resolve/supersede write via
// auditStatusChange (n4sn/r3m8v/1jkl5). So on a hub-connected sql-server crew, a
// responded/dismissed bead's close vanished from the durable record after a Dolt
// GC flatten.
//
// These complete the "all 4 legs" scope (the direct legs are covered by the
// embedded sibling). The fix emits auditStatusChange AFTER uw.Commit succeeds
// (a pre-commit emit would orphan the cwd-file entry on a tx rollback — r3m8v
// proxied precedent). End-to-end through the real proxied `bd` subprocess
// (BEADS_TEST_PROXIED_SERVER=1).
// MUTATION-VERIFIED: removing the auditStatusChange call in the respective
// proxied handler drops the entry → the parity assertion goes RED.

func TestProxiedHumanRespond_WritesGCSurvivableAuditTrail_mw44m(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)
	p := bdProxiedInit(t, bd, "phra")

	ctrl := bdProxiedCreate(t, bd, p.dir, "control close", "--type", "task")
	if out, err := bdProxiedRun(t, bd, p.dir, "close", ctrl.ID, "--reason", "control"); err != nil {
		t.Fatalf("CONTROL proxied close %s failed: %v\n%s", ctrl.ID, err, out)
	}
	if !auditHasStatusChange(t, p.dir, ctrl.ID, "closed") {
		t.Fatalf("CONTROL: proxied single-path close did not write a status field_change for %s — harness broken", ctrl.ID)
	}

	hb := bdProxiedCreate(t, bd, p.dir, "needs a human", "--type", "task", "--labels", "human")
	if out, err := bdProxiedRun(t, bd, p.dir, "human", "respond", hb.ID, "-r", "here is the answer"); err != nil {
		t.Fatalf("proxied `bd human respond` failed: %v\n%s", err, out)
	}
	if bdProxiedShow(t, bd, p.dir, hb.ID).Status != types.StatusClosed {
		t.Fatalf("proxied `bd human respond` did not close %s", hb.ID)
	}
	if !auditHasStatusChange(t, p.dir, hb.ID, "closed") {
		t.Errorf("proxied `bd human respond` did not write a GC-survivable audit field_change to status=closed for %s (beads-mw44m) — parity with single-path close broken", hb.ID)
	}
}

func TestProxiedHumanDismiss_WritesGCSurvivableAuditTrail_mw44m(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)
	p := bdProxiedInit(t, bd, "phda")

	ctrl := bdProxiedCreate(t, bd, p.dir, "control close", "--type", "task")
	if out, err := bdProxiedRun(t, bd, p.dir, "close", ctrl.ID, "--reason", "control"); err != nil {
		t.Fatalf("CONTROL proxied close %s failed: %v\n%s", ctrl.ID, err, out)
	}
	if !auditHasStatusChange(t, p.dir, ctrl.ID, "closed") {
		t.Fatalf("CONTROL: proxied single-path close did not write a status field_change for %s — harness broken", ctrl.ID)
	}

	hb := bdProxiedCreate(t, bd, p.dir, "needs a human", "--type", "task", "--labels", "human")
	if out, err := bdProxiedRun(t, bd, p.dir, "human", "dismiss", hb.ID, "--reason", "no longer applicable"); err != nil {
		t.Fatalf("proxied `bd human dismiss` failed: %v\n%s", err, out)
	}
	if bdProxiedShow(t, bd, p.dir, hb.ID).Status != types.StatusClosed {
		t.Fatalf("proxied `bd human dismiss` did not close %s", hb.ID)
	}
	if !auditHasStatusChange(t, p.dir, hb.ID, "closed") {
		t.Errorf("proxied `bd human dismiss` did not write a GC-survivable audit field_change to status=closed for %s (beads-mw44m) — parity with single-path close broken", hb.ID)
	}
}
