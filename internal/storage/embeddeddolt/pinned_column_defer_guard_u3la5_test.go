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

// TestPinnedColumnSurvivesLeavingPinnedStatus is the regression teeth for
// beads-y20w2 — the RESIDUAL leg u3la5's STATUS-keyed narrowing left open. An
// issue whose lifecycle STATUS is "pinned" AND which carries an independently-set
// pinned COLUMN (--pinned shield) must KEEP that column when it transitions away
// from pinned status. Entering the pinned STATUS never sets the column (only
// --pinned does; the two are orthogonal per beads-9ynk / cmd/bd/update.go:308-311),
// so the old status-pinned-EXIT auto-clear could only strip a legitimate shield =
// the exact silent prune/purge data-loss u3la5 protects against. y20w2 removed the
// EXIT-leg auto-clear in both the issueops seam and the domain/db proxied twin.
//
// NOTE: this REPLACES the former TestPinnedStatusLeaveClearsColumn, whose asserted
// outcome (column cleared on leaving pinned status) was itself the y20w2 bug — the
// fix intentionally inverts it. Explicit --no-pinned still clears the column
// (TestPinnedColumnClearsOnExplicitNoPinned).
func TestPinnedColumnSurvivesLeavingPinnedStatus(t *testing.T) {
	skipUnlessEmbeddedDolt(t)

	te := newTestEnv(t, "ps")
	ctx := t.Context()

	issue := &types.Issue{
		Title:     "status-pinned + shield",
		Status:    types.StatusPinned,
		Priority:  2,
		IssueType: types.TypeTask,
	}
	if err := te.store.CreateIssue(ctx, issue, "tester"); err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	// Set the independent --pinned shield on a status=pinned issue.
	if err := te.store.UpdateIssue(ctx, issue.ID, map[string]interface{}{"pinned": true}, "tester"); err != nil {
		t.Fatalf("UpdateIssue(pinned=true): %v", err)
	}

	// Transition away from pinned STATUS with NO --no-pinned: the shield must survive.
	if err := te.store.UpdateIssue(ctx, issue.ID, map[string]interface{}{"status": string(types.StatusOpen)}, "tester"); err != nil {
		t.Fatalf("UpdateIssue(status=open): %v", err)
	}

	var pinned bool
	te.queryScalar(t, ctx, "SELECT COALESCE(pinned, 0) FROM issues WHERE id = ?", []any{issue.ID}, &pinned)
	if !pinned {
		t.Errorf("pinned column (prune/purge shield) was silently stripped by leaving pinned STATUS (beads-y20w2); the column is orthogonal to status and must survive")
	}
}

// TestPinnedColumnClearsOnExplicitNoPinned confirms the column still clears when
// the caller explicitly asks for it (--no-pinned) during a status change — the
// column is managed solely by --pinned/--no-pinned, so explicit intent is honored.
func TestPinnedColumnClearsOnExplicitNoPinned(t *testing.T) {
	skipUnlessEmbeddedDolt(t)

	te := newTestEnv(t, "np")
	ctx := t.Context()

	issue := &types.Issue{
		Title:     "explicit no-pinned",
		Status:    types.StatusPinned,
		Priority:  2,
		IssueType: types.TypeTask,
	}
	if err := te.store.CreateIssue(ctx, issue, "tester"); err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if err := te.store.UpdateIssue(ctx, issue.ID, map[string]interface{}{"pinned": true}, "tester"); err != nil {
		t.Fatalf("UpdateIssue(pinned=true): %v", err)
	}

	// Explicitly clear the column in the same update that changes status.
	if err := te.store.UpdateIssue(ctx, issue.ID, map[string]interface{}{"status": string(types.StatusOpen), "pinned": false}, "tester"); err != nil {
		t.Fatalf("UpdateIssue(status=open, pinned=false): %v", err)
	}

	var pinned bool
	te.queryScalar(t, ctx, "SELECT COALESCE(pinned, 0) FROM issues WHERE id = ?", []any{issue.ID}, &pinned)
	if pinned {
		t.Errorf("explicit --no-pinned should clear the column even during a status change")
	}
}

// TestPinnedColumnUnsetStaysUnsetLeavingPinnedStatus is the no-regression teeth:
// a status=pinned issue WITHOUT the --pinned column set, transitioned to open,
// must leave the column false (no spurious write either way).
func TestPinnedColumnUnsetStaysUnsetLeavingPinnedStatus(t *testing.T) {
	skipUnlessEmbeddedDolt(t)

	te := newTestEnv(t, "pu")
	ctx := t.Context()

	issue := &types.Issue{
		Title:     "status-pinned no shield",
		Status:    types.StatusPinned,
		Priority:  2,
		IssueType: types.TypeTask,
	}
	if err := te.store.CreateIssue(ctx, issue, "tester"); err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	if err := te.store.UpdateIssue(ctx, issue.ID, map[string]interface{}{"status": string(types.StatusOpen)}, "tester"); err != nil {
		t.Fatalf("UpdateIssue(status=open): %v", err)
	}

	var pinned bool
	te.queryScalar(t, ctx, "SELECT COALESCE(pinned, 0) FROM issues WHERE id = ?", []any{issue.ID}, &pinned)
	if pinned {
		t.Errorf("pinned column should stay false when it was never set")
	}
}
