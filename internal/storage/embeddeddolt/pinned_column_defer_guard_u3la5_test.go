//go:build cgo

package embeddeddolt_test

import (
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestPinnedColumnSurvivesStatusChange is the regression teeth for beads-u3la5:
// the "Auto-clear pinned column when status transitions away from pinned" guard
// (internal/storage/issueops/update.go) must key on the OLD STATUS, not the
// pinned COLUMN. The pinned COLUMN (bd update --pinned) is an INDEPENDENT
// prune/purge protection marker orthogonal to the lifecycle status (beads-9ynk).
// Before the fix, deferring/blocking/reopening a column-pinned issue silently
// stripped the pinned column — deleting its prune/purge shield.
func TestPinnedColumnSurvivesStatusChange(t *testing.T) {
	skipUnlessEmbeddedDolt(t)

	pinnedCol := func(te *testEnv, t *testing.T, id string) bool {
		t.Helper()
		var pinned bool
		te.queryScalar(t, t.Context(),
			"SELECT COALESCE(pinned, 0) FROM issues WHERE id = ?", []any{id}, &pinned)
		return pinned
	}

	// Each subcase: create an open issue, set the pinned COLUMN via
	// update{pinned:true}, then apply a status-changing update and assert the
	// pinned column is STILL true.
	for _, tc := range []struct {
		name      string
		newStatus types.Status
	}{
		{"defer", types.StatusDeferred},
		{"block", types.StatusBlocked},
		{"in_progress", types.StatusInProgress},
	} {
		t.Run(tc.name, func(t *testing.T) {
			te := newTestEnv(t, "pc")
			ctx := t.Context()

			issue := &types.Issue{
				Title:     "protected " + tc.name,
				Status:    types.StatusOpen,
				Priority:  2,
				IssueType: types.TypeTask,
			}
			if err := te.store.CreateIssue(ctx, issue, "tester"); err != nil {
				t.Fatalf("CreateIssue: %v", err)
			}

			// Set the pinned COLUMN (prune/purge shield) — leaves status open.
			if err := te.store.UpdateIssue(ctx, issue.ID, map[string]interface{}{"pinned": true}, "tester"); err != nil {
				t.Fatalf("UpdateIssue(pinned=true): %v", err)
			}
			if !pinnedCol(te, t, issue.ID) {
				t.Fatalf("precondition: pinned column should be true after --pinned")
			}

			// A status-changing update that is NOT to "pinned" status.
			if err := te.store.UpdateIssue(ctx, issue.ID, map[string]interface{}{"status": string(tc.newStatus)}, "tester"); err != nil {
				t.Fatalf("UpdateIssue(status=%s): %v", tc.newStatus, err)
			}

			// The bug: the guard fired on oldIssue.Pinned==true and cleared the
			// column. It must survive — the pinned column is orthogonal to status.
			if !pinnedCol(te, t, issue.ID) {
				t.Errorf("pinned column was silently cleared by status change to %s (beads-u3la5); prune/purge shield lost", tc.newStatus)
			}
		})
	}
}

// TestPinnedStatusLeaveClearsColumn preserves the guard's ORIGINAL intent: an
// issue whose lifecycle STATUS is "pinned" and which then transitions away from
// pinned status should have the pinned column cleared. The fix keys on the old
// STATUS, so this behavior must remain.
func TestPinnedStatusLeaveClearsColumn(t *testing.T) {
	skipUnlessEmbeddedDolt(t)

	te := newTestEnv(t, "ps")
	ctx := t.Context()

	issue := &types.Issue{
		Title:     "status-pinned issue",
		Status:    types.StatusPinned,
		Priority:  2,
		IssueType: types.TypeTask,
	}
	if err := te.store.CreateIssue(ctx, issue, "tester"); err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	// Also set the column so we can observe the intended clear.
	if err := te.store.UpdateIssue(ctx, issue.ID, map[string]interface{}{"pinned": true}, "tester"); err != nil {
		t.Fatalf("UpdateIssue(pinned=true): %v", err)
	}

	// Transition away from pinned STATUS -> column should be auto-cleared.
	if err := te.store.UpdateIssue(ctx, issue.ID, map[string]interface{}{"status": string(types.StatusOpen)}, "tester"); err != nil {
		t.Fatalf("UpdateIssue(status=open): %v", err)
	}

	var pinned bool
	te.queryScalar(t, ctx, "SELECT COALESCE(pinned, 0) FROM issues WHERE id = ?", []any{issue.ID}, &pinned)
	if pinned {
		t.Errorf("pinned column should be cleared when leaving pinned STATUS (guard intent preserved)")
	}
}
