//go:build cgo

package main

import (
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// beads-1jkl5 (PROXIED leg): the proxied `bd gate resolve` runs through
// runGateResolveProxied, which closes the gate via uw.IssueUseCase().CloseIssue
// but — like the direct leg (gate.go) — dropped the GC-survivable audit-FILE
// trail (.beads/interactions.jsonl) that `bd close`/`bd update`/`bd supersede`/
// `bd duplicate` write via auditStatusChange (beads-n4sn / beads-r3m8v). So on a
// hub-connected sql-server crew, a resolved gate's close vanished from the
// durable record after a Dolt GC flatten.
//
// This completes the "both legs" scope (the direct leg is covered by the
// embedded sibling). The fix emits auditStatusChange AFTER uw.Commit succeeds
// (a pre-commit emit would orphan the cwd-file entry if the deferred uw.Close
// rolled the tx back — matching the r3m8v proxied fix). End-to-end through the
// real proxied `bd` subprocess (BEADS_TEST_PROXIED_SERVER=1).
// MUTATION-VERIFIED: removing the auditStatusChange call in runGateResolveProxied
// drops the entry → this parity assertion goes RED.

func TestProxiedGateResolve_WritesGCSurvivableAuditTrail(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)
	p := bdProxiedInit(t, bd, "pgra")

	// CONTROL: a single-path proxied close writes the status audit trail.
	ctrl := bdProxiedCreate(t, bd, p.dir, "control close", "--type", "task")
	if out, err := bdProxiedRun(t, bd, p.dir, "close", ctrl.ID, "--reason", "control"); err != nil {
		t.Fatalf("CONTROL proxied close %s failed: %v\n%s", ctrl.ID, err, out)
	}
	if !auditHasStatusChange(t, p.dir, ctrl.ID, "closed") {
		t.Fatalf("CONTROL: proxied single-path close did not write a status field_change for %s — harness broken", ctrl.ID)
	}

	// TEST: proxied gate resolve must write the SAME GC-survivable audit trail.
	target := bdProxiedCreate(t, bd, p.dir, "gate target", "--type", "task")
	gOut, err := bdProxiedRun(t, bd, p.dir, "gate", "create", "--blocks", target.ID, "--json")
	if err != nil {
		t.Fatalf("proxied `bd gate create` failed: %v\n%s", err, gOut)
	}
	gateIssue := parseIssueJSON(t, gOut)
	if gateIssue.ID == "" {
		t.Fatalf("proxied `bd gate create` did not return a gate ID\n%s", gOut)
	}

	if out, err := bdProxiedRun(t, bd, p.dir, "gate", "resolve", gateIssue.ID, "--reason", "done"); err != nil {
		t.Fatalf("proxied `bd gate resolve` failed: %v\n%s", err, out)
	}
	if bdProxiedShow(t, bd, p.dir, gateIssue.ID).Status != types.StatusClosed {
		t.Fatalf("proxied `bd gate resolve` did not close gate %s", gateIssue.ID)
	}
	if !auditHasStatusChange(t, p.dir, gateIssue.ID, "closed") {
		t.Errorf("proxied `bd gate resolve` did not write a GC-survivable audit field_change to status=closed for gate %s — parity with single-path close broken", gateIssue.ID)
	}
}
