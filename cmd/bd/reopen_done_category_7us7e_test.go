//go:build cgo

package main

import (
	"os"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestEmbeddedReopenDoneCategory_7us7e is the beads-7us7e teeth: `bd reopen`
// refused a custom DONE-category status (e.g. `bd config set status.custom
// "resolved:done"`, then `bd update --status resolved`). A done-category status
// is a terminal/complete outcome everywhere else in the x463g class (views
// exclude it; is_blocked/ready treat it as unblocking; the close guard + molecule
// progress + ship count it complete), so reopen — whose purpose is terminal->open
// — MUST apply to it exactly as to literal-closed. It did not, because:
//   - the cmd-layer guards (reopen.go / reopen_proxied_server.go) short-circuited
//     any non-literal-closed status into the advisory "not closed" no-op, and
//   - the TRUE gate, ReopenIssueInTx's CAS `WHERE id = ? AND status = 'closed'`,
//     0-row-matched a done-category status even if the cmd guards were widened.
//
// The fix widens the terminal test at all three seams to "literal-closed OR a
// done-category status" (FROZEN excluded — parked != done), keeping the CAS the
// load-bearing widening (`status IN (closed, <done names>)`). Degraded-safe: an
// empty done-set reduces to byte-identical literal-'closed' behavior.
//
// MUTATION-VERIFY: revert the CAS in issueops/reopen.go to `status = ?`
// (StatusClosed) → the done_category reopen subtests go RED (status stays in the
// done-category value, closed columns uncleared) while the negatives stay green.
func TestEmbeddedReopenDoneCategory_7us7e(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)

	// (1) reopening a done-category issue moves it to open + clears the close
	//     columns, exactly as literal-closed.
	t.Run("reopen_done_category_status_succeeds", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "rdc1")
		bdConfig(t, bd, dir, "set", "status.custom", "resolved:done")
		issue := bdCreate(t, bd, dir, "Done via custom status", "--type", "task")
		bdUpdate(t, bd, dir, issue.ID, "--status", "resolved")
		if got := bdShow(t, bd, dir, issue.ID); got.Status != types.Status("resolved") {
			t.Fatalf("precondition: issue should be in custom done status 'resolved', got %q", got.Status)
		}

		out := bdReopen(t, bd, dir, issue.ID)
		if !strings.Contains(out, "Reopened") {
			t.Errorf("expected 'Reopened' in output, got: %s", out)
		}
		got := bdShow(t, bd, dir, issue.ID)
		if got.Status != types.StatusOpen {
			t.Errorf("expected open after reopening a done-category issue, got %q", got.Status)
		}
		if got.ClosedAt != nil {
			t.Error("expected closed_at cleared after reopen")
		}
	})

	// (2) literal-closed still reopens (regression: the widened IN clause must
	//     still cover the baseline 'closed').
	t.Run("literal_closed_still_reopens", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "rdc2")
		bdConfig(t, bd, dir, "set", "status.custom", "resolved:done")
		issue := bdCreate(t, bd, dir, "Literal closed", "--type", "task")
		bdClose(t, bd, dir, issue.ID)
		bdReopen(t, bd, dir, issue.ID)
		if got := bdShow(t, bd, dir, issue.ID); got.Status != types.StatusOpen {
			t.Errorf("expected open after reopening a literal-closed issue, got %q", got.Status)
		}
	})

	// NEGATIVE (a): a FROZEN-category status is parked, NOT done — reopen must
	//     still treat it as a non-terminal advisory no-op (it stays put, exit 0).
	t.Run("frozen_category_status_is_not_reopened", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "rdc3")
		bdConfig(t, bd, dir, "set", "status.custom", "parked:frozen")
		issue := bdCreate(t, bd, dir, "Parked", "--type", "task")
		bdUpdate(t, bd, dir, issue.ID, "--status", "parked")
		// reopen is an advisory no-op here (exit 0), so bdReopen (expects rc=0).
		out := bdReopen(t, bd, dir, issue.ID)
		if strings.Contains(out, "Reopened") {
			t.Errorf("a frozen-category (parked) status must NOT be reopened, got: %s", out)
		}
		if got := bdShow(t, bd, dir, issue.ID); got.Status != types.Status("parked") {
			t.Errorf("frozen-category issue must stay parked, got %q", got.Status)
		}
	})

	// NEGATIVE (b): an in_progress issue is non-terminal — reopen still deliberately
	//     does not apply (advisory no-op, stays put), so the widening did not
	//     wholesale-enable reopen for every status.
	t.Run("in_progress_status_is_not_reopened", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "rdc4")
		bdConfig(t, bd, dir, "set", "status.custom", "resolved:done")
		issue := bdCreate(t, bd, dir, "Working", "--type", "task")
		bdUpdate(t, bd, dir, issue.ID, "--status", "in_progress")
		out := bdReopen(t, bd, dir, issue.ID)
		if strings.Contains(out, "Reopened") {
			t.Errorf("an in_progress issue must NOT be reopened, got: %s", out)
		}
		if got := bdShow(t, bd, dir, issue.ID); got.Status != types.StatusInProgress {
			t.Errorf("in_progress issue must stay in_progress, got %q", got.Status)
		}
	})

	// NEGATIVE (c): an already-open issue stays an idempotent no-op success
	//     (hxc2) — the widened guard must not turn this into an error.
	t.Run("already_open_stays_idempotent_noop", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "rdc5")
		bdConfig(t, bd, dir, "set", "status.custom", "resolved:done")
		issue := bdCreate(t, bd, dir, "Already open", "--type", "task")
		// bdReopen expects rc=0; an already-open reopen is a success no-op.
		_ = bdReopen(t, bd, dir, issue.ID)
		if got := bdShow(t, bd, dir, issue.ID); got.Status != types.StatusOpen {
			t.Errorf("already-open issue must stay open, got %q", got.Status)
		}
	})
}

// TestProxiedServerReopenDoneCategory_7us7e is the proxied-path twin: a
// hub-connected crew must get the same done-category reopen behavior (the cmd
// guard in reopen_proxied_server.go + the shared ReopenIssueInTx CAS, reached via
// issueSQLRepositoryImpl.Reopen). FROZEN still excluded; open still idempotent.
func TestProxiedServerReopenDoneCategory_7us7e(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	t.Run("reopen_done_category_status_succeeds", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "prdc1")
		bdProxiedConfig(t, bd, p.dir, "set", "status.custom", "resolved:done")
		issue := bdProxiedCreate(t, bd, p.dir, "Done via custom status")
		bdProxiedUpdate(t, bd, p.dir, issue.ID, "--status", "resolved")
		out := bdProxiedReopen(t, bd, p.dir, issue.ID)
		if !strings.Contains(out, "Reopened") {
			t.Errorf("expected 'Reopened' in stdout, got: %s", out)
		}
		db := openProxiedDB(t, p)
		if got := readStatus(t, db, issue.ID); got != types.StatusOpen {
			t.Errorf("status after done-category reopen: got %q, want open", got)
		}
	})

	t.Run("frozen_category_status_is_not_reopened", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "prdc2")
		bdProxiedConfig(t, bd, p.dir, "set", "status.custom", "parked:frozen")
		issue := bdProxiedCreate(t, bd, p.dir, "Parked")
		bdProxiedUpdate(t, bd, p.dir, issue.ID, "--status", "parked")
		// advisory no-op → exit 0, no reopen.
		out := bdProxiedReopen(t, bd, p.dir, issue.ID)
		if strings.Contains(out, "Reopened") {
			t.Errorf("a frozen-category (parked) status must NOT be reopened, got: %s", out)
		}
		db := openProxiedDB(t, p)
		if got := readStatus(t, db, issue.ID); got != types.Status("parked") {
			t.Errorf("frozen-category issue must stay parked, got %q", got)
		}
	})
}
