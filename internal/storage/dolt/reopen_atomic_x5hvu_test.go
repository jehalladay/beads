//go:build cgo

package dolt

import (
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestReopenIssueAtomic_x5hvu is the atomicity guard for beads-x5hvu on the
// server (DoltStore) backend: ReopenIssue must flip status=open AND write the
// documented reason-comment in ONE transaction (one Dolt commit). Before the fix
// it ran UpdateIssue (SQL tx + DOLT_COMMIT) and then AddIssueComment (a second
// tx + DOLT_COMMIT) as two committed transactions, so if the comment write
// failed the issue was reopened+committed with the reason silently lost and no
// rollback — the njnw (LinkAndClose) / pj38 (CompactOverwrite) split-state class.
// The fix routes both through the shared single-tx issueops.ReopenIssueInTx seam
// inside one withRetryTx + doltAddAndCommit.
//
// MUTATION-VERIFY: revert ReopenIssue to the two-call (UpdateIssue then
// AddIssueComment) form and reason_write_failure_rolls_back FAILS — the issue
// reopens (status=open) even though the comment write errored (split state).
func TestReopenIssueAtomic_x5hvu(t *testing.T) {
	// ── success: reopen flips status AND records the reason as a comment ─────
	t.Run("reopen_with_reason_atomic", func(t *testing.T) {
		store, cleanup := setupTestStore(t)
		defer cleanup()
		ctx, cancel := testContext(t)
		defer cancel()

		iss := &types.Issue{ID: "rox-1", Title: "Reopen me", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask}
		if err := store.CreateIssue(ctx, iss, "tester"); err != nil {
			t.Fatalf("CreateIssue: %v", err)
		}
		if err := store.CloseIssue(ctx, "rox-1", "done", "tester", ""); err != nil {
			t.Fatalf("CloseIssue: %v", err)
		}

		if err := store.ReopenIssue(ctx, "rox-1", "customer needs it back", "tester"); err != nil {
			t.Fatalf("ReopenIssue: %v", err)
		}

		var status string
		if err := store.db.QueryRowContext(ctx, "SELECT status FROM issues WHERE id = ?", "rox-1").Scan(&status); err != nil {
			t.Fatalf("query status: %v", err)
		}
		if status != string(types.StatusOpen) {
			t.Errorf("expected status=open after reopen, got %q", status)
		}
		var comments int
		if err := store.db.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM comments WHERE issue_id = ? AND text = ?",
			"rox-1", "customer needs it back").Scan(&comments); err != nil {
			t.Fatalf("query comments: %v", err)
		}
		if comments != 1 {
			t.Errorf("expected 1 reason comment, got %d", comments)
		}
	})

	// ── rollback: the reason-comment write fails → the reopen rolls back too.
	// Force the comment INSERT to fail by dropping the comments table; a
	// non-atomic (two-tx) implementation would have already committed status=open
	// before the comment write, leaving the issue reopened with the reason lost.
	t.Run("reason_write_failure_rolls_back", func(t *testing.T) {
		store, cleanup := setupTestStore(t)
		defer cleanup()
		ctx, cancel := testContext(t)
		defer cancel()

		iss := &types.Issue{ID: "roy-1", Title: "Reopen me", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask}
		if err := store.CreateIssue(ctx, iss, "tester"); err != nil {
			t.Fatalf("CreateIssue: %v", err)
		}
		if err := store.CloseIssue(ctx, "roy-1", "done", "tester", ""); err != nil {
			t.Fatalf("CloseIssue: %v", err)
		}

		// Break the reason-comment write leg.
		if _, err := store.db.ExecContext(ctx, "DROP TABLE comments"); err != nil {
			t.Fatalf("DROP TABLE comments: %v", err)
		}

		if err := store.ReopenIssue(ctx, "roy-1", "should not survive", "tester"); err == nil {
			t.Fatalf("expected ReopenIssue to fail when the reason-comment write fails")
		}

		var status string
		if err := store.db.QueryRowContext(ctx, "SELECT status FROM issues WHERE id = ?", "roy-1").Scan(&status); err != nil {
			t.Fatalf("query status: %v", err)
		}
		if status != string(types.StatusClosed) {
			t.Errorf("expected roy-1 to stay closed after failed reopen, got %q (split state!)", status)
		}
	})

	// ── control: reopen WITHOUT a reason still works (no comment leg) ─────────
	t.Run("reopen_no_reason_ok", func(t *testing.T) {
		store, cleanup := setupTestStore(t)
		defer cleanup()
		ctx, cancel := testContext(t)
		defer cancel()

		iss := &types.Issue{ID: "roz-1", Title: "Reopen me", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask}
		if err := store.CreateIssue(ctx, iss, "tester"); err != nil {
			t.Fatalf("CreateIssue: %v", err)
		}
		if err := store.CloseIssue(ctx, "roz-1", "done", "tester", ""); err != nil {
			t.Fatalf("CloseIssue: %v", err)
		}

		if err := store.ReopenIssue(ctx, "roz-1", "", "tester"); err != nil {
			t.Fatalf("ReopenIssue (no reason): %v", err)
		}
		var status string
		if err := store.db.QueryRowContext(ctx, "SELECT status FROM issues WHERE id = ?", "roz-1").Scan(&status); err != nil {
			t.Fatalf("query status: %v", err)
		}
		if status != string(types.StatusOpen) {
			t.Errorf("expected status=open after reopen, got %q", status)
		}
	})
}
