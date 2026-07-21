//go:build cgo

package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// bdBatch runs "bd batch" feeding `script` on stdin and returns stdout. It uses
// the REAL bd subprocess (not the runBatchScriptInTx tx-helper, which skips
// under embedded dolt because it needs a Dolt test server — and, more to the
// point, cannot exercise the cwd-based audit-FILE trail that only the real cmd
// handler writes). Fails the test on non-zero exit.
func bdBatch(t *testing.T, bd, dir, script string) string {
	t.Helper()
	cmd := exec.Command(bd, "batch")
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	cmd.Stdin = strings.NewReader(script)
	stdout, stderr, err := runCommandBuffers(t, cmd)
	if err != nil {
		t.Fatalf("bd batch failed: %v\nscript:\n%s\nstdout:\n%s\nstderr:\n%s", err, script, stdout.String(), stderr.String())
	}
	return stdout.String()
}

// TestEmbeddedBatchAuditTrail_c2pr1 proves the batch handler writes the
// GC-survivable audit-file trail (.beads/interactions.jsonl) for status
// transitions, at parity with the single-invocation close/update paths
// (beads-c2pr1, extends the beads-n4sn class to the batch handler).
//
// The batch close leg (tx.CloseIssue) and update leg (tx.UpdateIssue) wrote the
// Dolt event row but never emitted audit.LogFieldChange, so a loop→batch
// refactor (`bd list -q | awk '{print "close",$1}' | bd batch`) silently dropped
// the durable close-audit trail for every issue it closed. After a Dolt GC
// flatten (which destroys commit history) the transition would be gone.
func TestEmbeddedBatchAuditTrail_c2pr1(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "bc")

	// ===== CONTROL: single-path close writes the audit trail (baseline) =====

	t.Run("control_single_close_writes_audit_trail", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "single close", "--type", "task")
		cmd := exec.Command(bd, "close", issue.ID, "-r", "single reason")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		if _, stderr, err := runCommandBuffers(t, cmd); err != nil {
			t.Fatalf("bd close %s failed: %v\nstderr:\n%s", issue.ID, err, stderr.String())
		}
		if !auditHasStatusChange(t, dir, issue.ID, "closed") {
			t.Fatalf("CONTROL: single-path close did not write a status field_change audit entry for %s — harness broken", issue.ID)
		}
	})

	// ===== TEST: batch close must write the SAME audit trail (parity) =====

	t.Run("batch_close_writes_gc_survivable_audit_trail", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "batch close", "--type", "task")
		bdBatch(t, bd, dir, "close "+issue.ID+" batch reason\n")

		if getIssueStatus(t, bd, dir, issue.ID) != "closed" {
			t.Fatalf("batch close did not close %s", issue.ID)
		}
		if !auditHasStatusChange(t, dir, issue.ID, "closed") {
			t.Errorf("batch close did not write a GC-survivable audit field_change to status=closed for %s (beads-c2pr1) — parity with single-path close broken", issue.ID)
		}
	})

	// ===== TEST: batch update status=closed writes the audit trail =====

	t.Run("batch_update_status_closed_writes_audit_trail", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "batch update to closed", "--type", "task")
		bdBatch(t, bd, dir, "update "+issue.ID+" status=closed\n")

		if getIssueStatus(t, bd, dir, issue.ID) != "closed" {
			t.Fatalf("batch update status=closed did not close %s", issue.ID)
		}
		if !auditHasStatusChange(t, dir, issue.ID, "closed") {
			t.Errorf("batch update status=closed did not write a GC-survivable audit field_change for %s (beads-c2pr1) — parity with bd update --status closed broken", issue.ID)
		}
	})

	// ===== TEST: batch update status=in_progress writes the audit trail =====

	t.Run("batch_update_status_in_progress_writes_audit_trail", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "batch update to in_progress", "--type", "task")
		bdBatch(t, bd, dir, "update "+issue.ID+" status=in_progress\n")

		if getIssueStatus(t, bd, dir, issue.ID) != "in_progress" {
			t.Fatalf("batch update status=in_progress did not transition %s", issue.ID)
		}
		if !auditHasStatusChange(t, dir, issue.ID, "in_progress") {
			t.Errorf("batch update status=in_progress did not write a GC-survivable audit field_change for %s (beads-c2pr1)", issue.ID)
		}
	})

	// ===== No orphan audit on rollback: a batch that fails mid-way must NOT
	// leave an audit-file entry for the op that was rolled back. =====

	t.Run("batch_rollback_writes_no_orphan_audit", func(t *testing.T) {
		good := bdCreate(t, bd, dir, "rollback good", "--type", "task")
		// Second op references a nonexistent id → whole tx rolls back, good stays open.
		script := "close " + good.ID + " ok\nclose bc-nonexistent-zzz should-fail\n"
		cmd := exec.Command(bd, "batch")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		cmd.Stdin = strings.NewReader(script)
		_, _, err := runCommandBuffers(t, cmd)
		if err == nil {
			t.Fatalf("expected batch to fail on nonexistent id (whole-tx rollback)")
		}
		if getIssueStatus(t, bd, dir, good.ID) != "open" {
			t.Fatalf("rollback did not restore %s to open — tx semantics changed", good.ID)
		}
		if auditHasStatusChange(t, dir, good.ID, "closed") {
			t.Errorf("batch rollback left an ORPHAN audit field_change to status=closed for %s — audit must be flushed only after the tx commits (beads-c2pr1)", good.ID)
		}
	})
}
