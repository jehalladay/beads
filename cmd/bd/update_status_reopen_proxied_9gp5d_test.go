//go:build cgo

package main

import (
	"context"
	"database/sql"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestProxiedUpdateStatusReopenClearsAllThree_9gp5d is the teeth for beads-9gp5d:
// the PROXIED (domain/db) `bd update --status open` reopen leg must clear ALL
// THREE close-provenance columns — closed_at, close_reason, closed_by_session —
// matching the direct twin (issueops.ManageClosedAt reopen branch, beads-ni2ph).
// Before the fix the proxied inline reopen leg cleared ONLY closed_at, leaving a
// contradictory "open but closed_by_session=X / close_reason=Closed" row on a
// hub-connected / proxied-server client.
//
// This is the status-update reopen path (bd update --status open →
// issueUseCaseImpl.update → issueRepo.Update inline leg), NOT `bd reopen`, which
// correctly delegates to issueops.ReopenIssueInTx (covered by
// TestProxiedServerReopen).
func TestProxiedUpdateStatusReopenClearsAllThree_9gp5d(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	readCol := func(t *testing.T, db *sql.DB, id, col string) (sql.NullString, sql.NullTime) {
		t.Helper()
		var s sql.NullString
		var ts sql.NullTime
		var err error
		if col == "closed_at" {
			err = db.QueryRowContext(context.Background(),
				"SELECT closed_at FROM issues WHERE id = ?", id).Scan(&ts)
		} else {
			//nolint:gosec // col is one of two hardcoded literals below
			err = db.QueryRowContext(context.Background(),
				"SELECT "+col+" FROM issues WHERE id = ?", id).Scan(&s)
		}
		if err != nil {
			t.Fatalf("read %s for %s: %v", col, id, err)
		}
		return s, ts
	}

	p := bdProxiedInit(t, bd, "upro")
	a := bdProxiedCreate(t, bd, p.dir, "Reopen via update", "--type", "task")

	// Close it via `bd update --status closed` so close_reason defaults to
	// "Closed" (beads-6qo8t) and set closed_by_session explicitly — both must be
	// non-empty for the reopen-clear to be observable.
	if _, err := bdProxiedRun(t, bd, p.dir, "update", a.ID, "--status", "closed", "--session", "sess-9gp5d"); err != nil {
		t.Fatalf("update --status closed failed: %v", err)
	}
	// Sanity: the three columns are populated before reopen.
	if cr, _ := readCol(t, openProxiedDB(t, p), a.ID, "close_reason"); cr.String == "" {
		t.Fatalf("precondition: close_reason should be populated after close, got empty")
	}
	if cbs, _ := readCol(t, openProxiedDB(t, p), a.ID, "closed_by_session"); cbs.String != "sess-9gp5d" {
		t.Fatalf("precondition: closed_by_session = %q, want sess-9gp5d", cbs.String)
	}

	// Reopen via the status-update path.
	if _, err := bdProxiedRun(t, bd, p.dir, "update", a.ID, "--status", "open"); err != nil {
		t.Fatalf("update --status open failed: %v", err)
	}

	db := openProxiedDB(t, p)
	if cr, _ := readCol(t, db, a.ID, "close_reason"); cr.Valid && cr.String != "" {
		t.Errorf("close_reason not cleared on reopen: got %q, want empty", cr.String)
	}
	// RED before the fix: closed_by_session stayed "sess-9gp5d".
	if cbs, _ := readCol(t, db, a.ID, "closed_by_session"); cbs.Valid && cbs.String != "" {
		t.Errorf("closed_by_session not cleared on reopen: got %q, want empty (ni2ph twin gap)", cbs.String)
	}
	if _, closedAt := readCol(t, db, a.ID, "closed_at"); closedAt.Valid {
		t.Errorf("closed_at not cleared on reopen: got %v", closedAt.Time)
	}
	if got := readStatus(t, db, a.ID); got != types.StatusOpen {
		t.Errorf("status = %q, want open", got)
	}
}
