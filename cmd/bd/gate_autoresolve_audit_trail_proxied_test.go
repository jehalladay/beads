//go:build cgo

package main

import (
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/types"
)

// beads-8ociu (PROXIED leg): the proxied `bd gate check` auto-resolve runs through
// closeGateProxied, which closes the gate via uw.IssueUseCase().CloseIssue but —
// like the direct leg (closeGate) — dropped the GC-survivable audit-FILE trail
// (.beads/interactions.jsonl) that manual `bd gate resolve` writes via
// auditStatusChange (beads-1jkl5). So on a hub-connected sql-server crew, an
// auto-resolved gate's close vanished from the durable record after a Dolt GC
// flatten. The fix emits auditStatusChange AFTER uw.Commit (r3m8v precedent).
// End-to-end through the real proxied `bd` subprocess (BEADS_TEST_PROXIED_SERVER=1).
// MUTATION-VERIFIED: removing the auditStatusChange call in closeGateProxied drops
// the entry → the parity assertion goes RED.

func TestProxiedGateCheckAutoResolve_WritesGCSurvivableAuditTrail_8ociu(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)
	p := bdProxiedInit(t, bd, "pgar")

	ctrl := bdProxiedCreate(t, bd, p.dir, "control close", "--type", "task")
	if out, err := bdProxiedRun(t, bd, p.dir, "close", ctrl.ID, "--reason", "control"); err != nil {
		t.Fatalf("CONTROL proxied close %s failed: %v\n%s", ctrl.ID, err, out)
	}
	if !auditHasStatusChange(t, p.dir, ctrl.ID, "closed") {
		t.Fatalf("CONTROL: proxied single-path close did not write a status field_change for %s — harness broken", ctrl.ID)
	}

	target := bdProxiedCreate(t, bd, p.dir, "gate target", "--type", "task")
	gOut, err := bdProxiedRun(t, bd, p.dir, "gate", "create", "--type", "timer", "--timeout", "1s", "--blocks", target.ID, "--json")
	if err != nil {
		t.Fatalf("proxied `bd gate create --type timer` failed: %v\n%s", err, gOut)
	}
	gateIssue := parseIssueJSON(t, gOut)
	if gateIssue.ID == "" {
		t.Fatalf("proxied `bd gate create` did not return a gate ID\n%s", gOut)
	}

	time.Sleep(1500 * time.Millisecond)
	if out, err := bdProxiedRun(t, bd, p.dir, "gate", "check", "--type", "timer"); err != nil {
		t.Fatalf("proxied `bd gate check --type timer` failed: %v\n%s", err, out)
	}
	if bdProxiedShow(t, bd, p.dir, gateIssue.ID).Status != types.StatusClosed {
		t.Fatalf("proxied `bd gate check` did not auto-resolve (close) timer gate %s", gateIssue.ID)
	}
	if !auditHasStatusChange(t, p.dir, gateIssue.ID, "closed") {
		t.Errorf("proxied `bd gate check` auto-resolve did not write a GC-survivable audit field_change to status=closed for gate %s (beads-8ociu) — parity with manual resolve broken", gateIssue.ID)
	}
}
