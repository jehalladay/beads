//go:build cgo

package main

import (
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// beads-iwzua: `bd epic close-eligible` closes each eligible epic via
// renderEpicCloseEligible's closeFn (direct store.CloseIssue / proxied
// issueUC.CloseIssue) but neither leg wrote the GC-survivable
// .beads/interactions.jsonl audit-file trail (beads-n4sn) that `bd close`
// writes. So an epic auto-closed by close-eligible left only the DB EventClosed
// row (destroyed by a Dolt GC flatten) — the same n4sn divergence r3m8v/zt47w/
// 1jkl5 exist to close, on the epic-close-eligible leg. The fix emits
// auditStatusChange per closed epic at the shared chokepoint after commit.
//
// End-to-end through the real `bd` subprocess: the cwd-based audit FILE is only
// written by the real cmd handler (c2pr1 lesson). Reuses bdDep/bdClose/bdEpic/
// bdShow + auditHasStatusChange from the sibling family tests.
//
// MUTATION-VERIFIED: removing the auditStatusChange loop in
// renderEpicCloseEligible → this test goes RED (epic closes in the DB but no
// file-trail entry).

func TestEpicCloseEligible_WritesGCSurvivableAuditTrail_iwzua(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "ece")

	// CONTROL: a plain `bd close` writes the status audit trail — proves the
	// harness detects the entry a single-path close emits.
	ctrl := bdCreate(t, bd, dir, "control close", "--type", "task")
	bdClose(t, bd, dir, ctrl.ID)
	if !auditHasStatusChange(t, dir, ctrl.ID, "closed") {
		t.Fatalf("CONTROL: single-path close did not write a status field_change for %s — harness broken", ctrl.ID)
	}

	// Seed an epic whose only child is closed → eligible for closure but still
	// open until close-eligible runs (mirrors epic_embedded_test).
	epic := bdCreate(t, bd, dir, "close me epic", "--type", "epic")
	child := bdCreate(t, bd, dir, "close me child", "--type", "task")
	bdDep(t, bd, dir, "add", child.ID, epic.ID, "--type", "parent-child")
	bdClose(t, bd, dir, child.ID)
	if got := bdShow(t, bd, dir, epic.ID); got.Status != types.StatusOpen {
		t.Fatalf("epic %s should stay open until close-eligible runs, got %s", epic.ID, got.Status)
	}

	// Close the eligible epic via `bd epic close-eligible`.
	bdEpic(t, bd, dir, "close-eligible")
	if got := bdShow(t, bd, dir, epic.ID); got.Status != types.StatusClosed {
		t.Fatalf("`bd epic close-eligible` did not close epic %s (status=%s)", epic.ID, got.Status)
	}

	// The closed epic must get its own GC-survivable field_change entry, at
	// parity with a plain close (beads-iwzua).
	if !auditHasStatusChange(t, dir, epic.ID, "closed") {
		t.Errorf("epic %s closed by `bd epic close-eligible` has NO GC-survivable audit field_change to status=closed (beads-iwzua) — invisible after a Dolt GC flatten while a plain close is not", epic.ID)
	}
}
