//go:build cgo

package main

import (
	"bytes"
	"context"
	"os"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// beads-sxgz3: `bd ado sync` reconciliation auto-closes local issues whose ADO
// work item was deleted (404) via store.CloseIssue directly — the same
// CloseIssue-bypass audit-file-trail parity gap as gate-resolve (1jkl5),
// gate-check (8ociu), supersede/duplicate (r3m8v), and the autoclose cascade
// (zt47w). A reconcile-driven close recorded only the DB EventClosed row, NOT
// the GC-survivable .beads/interactions.jsonl field_change entry (beads-n4sn)
// that the canonical `bd close` writes at close.go:217 via auditStatusChange.
// So after a Dolt GC flatten (which destroys commit history) a reconcile-closed
// issue's close vanished from the durable record while an explicitly-closed
// issue's did not.
//
// The reconcile-close loop was factored into closeReconciledDeletedIssues so
// the emit is exercised directly against a real embedded store + the cwd-based
// audit FILE (only the real cmd handler writes that file — the c2pr1/r3m8v/zt47w
// lesson), without a full multi-endpoint `bd ado sync` mock ADO server.
// Uses the embedded-dolt harness (bdInit + embeddeddolt.Open), no Docker.
//
// MUTATION-VERIFIED: removing the auditStatusChange(localID, ...) call in
// closeReconciledDeletedIssues → the trail assertion goes RED (issue closes in
// the DB but leaves no GC-survivable field_change entry).
func TestADOReconcileClose_WritesGCSurvivableAuditTrail_sxgz3(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}

	bd := buildEmbeddedBD(t)
	dir, beadsDir, _ := bdInit(t, bd, "--prefix", "ado")

	// Seed an OPEN issue via the real embedded store (same DB the CLI uses),
	// mapped to a (now-deleted) ADO work item #4242.
	st := openStore(t, beadsDir, "ado")
	ctx := context.Background()
	const localID = "ado-1"
	issue := &types.Issue{
		ID:        localID,
		Title:     "reconcile close audit target",
		Status:    types.StatusOpen,
		Priority:  2,
		IssueType: types.TypeTask,
	}
	if err := st.CreateIssue(ctx, issue, "test-actor"); err != nil {
		t.Fatalf("seed issue %s: %v", localID, err)
	}

	// The audit FILE resolves relative to cwd (.beads/interactions.jsonl), so
	// chdir into the initialized workspace before invoking the close path —
	// same as the close_routing / autoclose family tests.
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWD) })
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir workspace: %v", err)
	}

	adoIDMap := map[int]string{4242: localID}
	var warnings []string
	var out bytes.Buffer

	closeReconciledDeletedIssues(ctx, st, "test-actor", []string{"4242"}, adoIDMap, &out, &warnings)

	// Precondition: the issue actually auto-closed (DB side).
	closed, err := st.GetIssue(ctx, localID)
	if err != nil {
		t.Fatalf("GetIssue(%s): %v", localID, err)
	}
	if closed == nil || closed.Status != types.StatusClosed {
		var got types.Status
		if closed != nil {
			got = closed.Status
		}
		t.Fatalf("reconcile did not close %s (status=%q) — close precondition broken", localID, got)
	}

	// The reconcile-driven close must write the GC-survivable field_change
	// entry, at parity with the canonical `bd close` (beads-sxgz3 / n4sn).
	if !auditHasStatusChange(t, dir, localID, "closed") {
		t.Errorf("reconcile-closed %s has NO GC-survivable audit field_change to status=closed (beads-sxgz3) — the close is invisible after a Dolt GC flatten while an explicit `bd close` of the same issue is not", localID)
	}
}
