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

// TestEmbeddedBatchAuditTrail_qeb2p proves the batch update leg writes the
// GC-survivable audit-file trail for the assignee and priority fields, at
// parity with single-invocation `bd update --assignee`/`--priority` (which emit
// via auditIssueUpdate, audit_field_changes.go:32 → LogFieldChange for
// status/assignee/priority).
//
// beads-c2pr1 fixed batch's audit-file gap for STATUS only. The batch update
// leg also handles assignee= and priority= (parseUpdateKVs), and those
// transitions wrote the Dolt event row + column but dropped the durable
// audit-FILE entry — after a Dolt GC flatten the assignee/priority change would
// be gone from the durable record, at odds with single-path update. qeb2p
// generalizes the capture to the full auditIssueUpdate field set.
func TestEmbeddedBatchAuditTrail_qeb2p(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "bq")

	// ===== CONTROL: single-path update --assignee/--priority write the trail. =====

	t.Run("control_single_update_assignee_priority_writes_audit_trail", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "single update", "--type", "task")
		cmd := exec.Command(bd, "update", issue.ID, "--assignee", "ctrl-alice", "--priority", "1")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		if _, stderr, err := runCommandBuffers(t, cmd); err != nil {
			t.Fatalf("bd update %s failed: %v\nstderr:\n%s", issue.ID, err, stderr.String())
		}
		if !auditHasFieldChange(t, dir, issue.ID, "assignee", "ctrl-alice") {
			t.Fatalf("CONTROL: single-path update did not write an assignee field_change for %s — harness broken", issue.ID)
		}
		if !auditHasFieldChange(t, dir, issue.ID, "priority", "1") {
			t.Fatalf("CONTROL: single-path update did not write a priority field_change for %s — harness broken", issue.ID)
		}
	})

	// ===== TEST: batch update assignee= must write the SAME audit trail. =====

	t.Run("batch_update_assignee_writes_gc_survivable_audit_trail", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "batch assignee", "--type", "task")
		bdBatch(t, bd, dir, "update "+issue.ID+" assignee=batch-bob\n")

		if !auditHasFieldChange(t, dir, issue.ID, "assignee", "batch-bob") {
			t.Errorf("batch update assignee= did not write a GC-survivable audit field_change for %s (beads-qeb2p) — parity with bd update --assignee broken", issue.ID)
		}
	})

	// ===== TEST: batch update priority= must write the SAME audit trail. =====

	t.Run("batch_update_priority_writes_gc_survivable_audit_trail", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "batch priority", "--type", "task")
		bdBatch(t, bd, dir, "update "+issue.ID+" priority=0\n")

		if !auditHasFieldChange(t, dir, issue.ID, "priority", "0") {
			t.Errorf("batch update priority= did not write a GC-survivable audit field_change for %s (beads-qeb2p) — parity with bd update --priority broken", issue.ID)
		}
	})

	// ===== TEST: a combined assignee+priority+status batch update writes ALL
	// three field_change entries (auditIssueUpdate diffs the full updates map). =====

	t.Run("batch_update_combined_writes_all_field_changes", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "batch combined", "--type", "task")
		// priority=0 (not the default 2) so the value actually changes — a
		// priority=default update is a no-op that LogFieldChange correctly skips
		// (audit.go:181, old==new), matching single-path `bd update`.
		bdBatch(t, bd, dir, "update "+issue.ID+" assignee=combo-carol priority=0 status=in_progress\n")

		if !auditHasFieldChange(t, dir, issue.ID, "assignee", "combo-carol") {
			t.Errorf("combined batch update dropped the assignee audit field_change for %s (beads-qeb2p)", issue.ID)
		}
		if !auditHasFieldChange(t, dir, issue.ID, "priority", "0") {
			t.Errorf("combined batch update dropped the priority audit field_change for %s (beads-qeb2p)", issue.ID)
		}
		if !auditHasStatusChange(t, dir, issue.ID, "in_progress") {
			t.Errorf("combined batch update dropped the status audit field_change for %s (beads-c2pr1 regression)", issue.ID)
		}
	})

	// ===== No orphan audit on rollback: a batch update that fails mid-way must
	// NOT leave an assignee/priority audit entry for the rolled-back op. =====

	t.Run("batch_update_rollback_writes_no_orphan_audit", func(t *testing.T) {
		good := bdCreate(t, bd, dir, "rollback assignee good", "--type", "task")
		script := "update " + good.ID + " assignee=rollback-dave\nclose bq-nonexistent-zzz should-fail\n"
		cmd := exec.Command(bd, "batch")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		cmd.Stdin = strings.NewReader(script)
		if _, _, err := runCommandBuffers(t, cmd); err == nil {
			t.Fatalf("expected batch to fail on nonexistent id (whole-tx rollback)")
		}
		if auditHasFieldChange(t, dir, good.ID, "assignee", "rollback-dave") {
			t.Errorf("batch rollback left an ORPHAN assignee audit field_change for %s — audit must be flushed only after the tx commits (beads-qeb2p)", good.ID)
		}
	})
}
