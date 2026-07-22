//go:build cgo

package embeddeddolt_test

import (
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestReopenIssueAtomic_x5hvu is the atomicity guard for beads-x5hvu:
// EmbeddedDoltStore.ReopenIssue must flip status=open AND write the documented
// reason-comment in ONE transaction. Before the fix it ran UpdateIssue and
// AddIssueComment as two separate auto-committed transactions, so if the comment
// write failed the issue was reopened+committed with the reason silently lost
// and no rollback — a split state (same class as njnw LinkAndClose / pj38
// CompactOverwrite). The fix routes both through the shared single-tx
// issueops.ReopenIssueInTx seam inside one withConn(true) closure.
//
// MUTATION-VERIFY: revert ReopenIssue to the two-call
// (UpdateIssue then AddIssueComment) form and the reason_write_failure_rolls_back
// subtest FAILS — the issue reopens (status=open) even though the comment write
// errored, exactly the split state.
func TestReopenIssueAtomic_x5hvu(t *testing.T) {
	skipUnlessEmbeddedDolt(t)

	// ── success: reopen flips status AND records the reason as a comment ─────
	t.Run("reopen_with_reason_atomic", func(t *testing.T) {
		te := newTestEnv(t, "rox")
		ctx := t.Context()

		iss := &types.Issue{ID: "rox-1", Title: "Reopen me", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask}
		if err := te.store.CreateIssue(ctx, iss, "tester"); err != nil {
			t.Fatalf("CreateIssue: %v", err)
		}
		if err := te.store.CloseIssue(ctx, "rox-1", "done", "tester", ""); err != nil {
			t.Fatalf("CloseIssue: %v", err)
		}

		if err := te.store.ReopenIssue(ctx, "rox-1", "customer needs it back", "tester"); err != nil {
			t.Fatalf("ReopenIssue: %v", err)
		}

		var status string
		te.queryScalar(t, ctx, "SELECT status FROM issues WHERE id = ?", []any{"rox-1"}, &status)
		if status != string(types.StatusOpen) {
			t.Errorf("expected status=open after reopen, got %q", status)
		}
		// The reason must be present in the COMMENTS table (beads-bimd0).
		var comments int
		te.queryScalar(t, ctx,
			"SELECT COUNT(*) FROM comments WHERE issue_id = ? AND text = ?",
			[]any{"rox-1", "customer needs it back"}, &comments)
		if comments != 1 {
			t.Errorf("expected 1 reason comment, got %d", comments)
		}
	})

	// ── rollback: the reason-comment write fails → the reopen rolls back too.
	// This is the exact split-state x5hvu fixes. We force the comment INSERT to
	// fail by dropping the comments table first; a non-atomic (two-tx)
	// implementation would have already committed status=open before the comment
	// write, leaving the issue reopened with the reason lost.
	t.Run("reason_write_failure_rolls_back", func(t *testing.T) {
		te := newTestEnv(t, "roy")
		ctx := t.Context()

		iss := &types.Issue{ID: "roy-1", Title: "Reopen me", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask}
		if err := te.store.CreateIssue(ctx, iss, "tester"); err != nil {
			t.Fatalf("CreateIssue: %v", err)
		}
		if err := te.store.CloseIssue(ctx, "roy-1", "done", "tester", ""); err != nil {
			t.Fatalf("CloseIssue: %v", err)
		}

		// Break the reason-comment write leg.
		te.exec(t, ctx, "DROP TABLE comments")

		if err := te.store.ReopenIssue(ctx, "roy-1", "should not survive", "tester"); err == nil {
			t.Fatalf("expected ReopenIssue to fail when the reason-comment write fails")
		}

		// The status flip must NOT survive — it was in the same tx that failed at
		// the comment write. A non-atomic implementation would leave it open.
		var status string
		te.queryScalar(t, ctx, "SELECT status FROM issues WHERE id = ?", []any{"roy-1"}, &status)
		if status != string(types.StatusClosed) {
			t.Errorf("expected roy-1 to stay closed after failed reopen, got %q (split state!)", status)
		}
	})

	// ── control: reopen WITHOUT a reason still works (no comment leg) ─────────
	t.Run("reopen_no_reason_ok", func(t *testing.T) {
		te := newTestEnv(t, "roz")
		ctx := t.Context()

		iss := &types.Issue{ID: "roz-1", Title: "Reopen me", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask}
		if err := te.store.CreateIssue(ctx, iss, "tester"); err != nil {
			t.Fatalf("CreateIssue: %v", err)
		}
		if err := te.store.CloseIssue(ctx, "roz-1", "done", "tester", ""); err != nil {
			t.Fatalf("CloseIssue: %v", err)
		}

		if err := te.store.ReopenIssue(ctx, "roz-1", "", "tester"); err != nil {
			t.Fatalf("ReopenIssue (no reason): %v", err)
		}
		var status string
		te.queryScalar(t, ctx, "SELECT status FROM issues WHERE id = ?", []any{"roz-1"}, &status)
		if status != string(types.StatusOpen) {
			t.Errorf("expected status=open after reopen, got %q", status)
		}
	})
}
