//go:build cgo

package dolt

import (
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// beads-ni2ph: closed_by_session was a WRITE-ONLY column. bd close --session
// (CloseIssue's final arg) persisted it, but it was ABSENT from the canonical
// hydration list (sqlbuild.IssueSelectColumns) and the positional
// issueops.ScanIssueFrom, so every read path (GetIssue, SearchIssues, export,
// bd show --json) dropped it — issue.ClosedBySession was ALWAYS empty on read.
// close_reason (its sibling in the same close SET clause) round-tripped fine;
// closed_by_session was the odd one out. Same hydration-drift class as
// beads-kyr9q / beads-5rn1c, but a column missing from the SHARED canonical
// SELECT, so it hit all readers at once.
//
// These teeth pin the round trip on the real embedded store. RED before the
// fix (empty on read), GREEN after adding the column to IssueSelectColumns +
// the matching positional dest/map in ScanIssueFrom.

// A closed permanent issue must hydrate its closed_by_session on the canonical
// GetIssue read path (which builds its SELECT from IssueSelectColumns and scans
// via ScanIssueFrom — the exact seam the bug lived in).
func TestClosedBySession_GetIssue_RoundTrips_ni2ph(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()
	ctx, cancel := testContext(t)
	defer cancel()

	createPerm(t, ctx, store, "cbs-perm")
	if err := store.CloseIssue(ctx, "cbs-perm", "done", "tester", "sess-ROUNDTRIP-999"); err != nil {
		t.Fatalf("CloseIssue --session: %v", err)
	}

	got, err := store.GetIssue(ctx, "cbs-perm")
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if got.ClosedBySession != "sess-ROUNDTRIP-999" {
		t.Errorf("ClosedBySession = %q, want %q (write-only column never hydrated — canonical SELECT/scan drift)", got.ClosedBySession, "sess-ROUNDTRIP-999")
	}
	// Regression guard on the positional scan: close_reason (the sibling in the
	// same SET clause, one column earlier in IssueSelectColumns) must stay
	// aligned after the insertion.
	if got.CloseReason != "done" {
		t.Errorf("CloseReason = %q, want %q (positional scan alignment regressed)", got.CloseReason, "done")
	}
}

// The wisps table shares IssueSelectColumns (getIssueFromTableInTx reads wisps
// with the same list) and the same DDL family, so a closed wisp must hydrate
// its session too.
func TestClosedBySession_GetIssue_RoundTrips_Wisp_ni2ph(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()
	ctx, cancel := testContext(t)
	defer cancel()

	createWisp(t, ctx, store, "cbs-wisp")
	if err := store.CloseIssue(ctx, "cbs-wisp", "done", "tester", "sess-WISP-777"); err != nil {
		t.Fatalf("CloseIssue wisp --session: %v", err)
	}

	got, err := store.GetIssue(ctx, "cbs-wisp")
	if err != nil {
		t.Fatalf("GetIssue wisp: %v", err)
	}
	if got.ClosedBySession != "sess-WISP-777" {
		t.Errorf("wisp ClosedBySession = %q, want %q", got.ClosedBySession, "sess-WISP-777")
	}
}

// SearchIssues (the read path behind bd list / bd search / export, which
// serializes the hydrated issue) builds its SELECT from IssueSelectColumns and
// scans via ScanIssueFrom too — it must carry the session for the closed row.
func TestClosedBySession_SearchIssues_RoundTrips_ni2ph(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()
	ctx, cancel := testContext(t)
	defer cancel()

	createPerm(t, ctx, store, "cbs-search")
	if err := store.CloseIssue(ctx, "cbs-search", "done", "tester", "sess-SEARCH-555"); err != nil {
		t.Fatalf("CloseIssue --session: %v", err)
	}

	issues, err := store.SearchIssues(ctx, "", types.IssueFilter{IDs: []string{"cbs-search"}})
	if err != nil {
		t.Fatalf("SearchIssues: %v", err)
	}
	if len(issues) != 1 {
		t.Fatalf("SearchIssues returned %d issues, want 1", len(issues))
	}
	if issues[0].ClosedBySession != "sess-SEARCH-555" {
		t.Errorf("SearchIssues ClosedBySession = %q, want %q (list/export read path drops the column)", issues[0].ClosedBySession, "sess-SEARCH-555")
	}
}

// Reopen writes closed_by_session='' (reopen.go); with the read fix the clear
// is now observable — a reopened issue must hydrate an EMPTY session, not a
// stale one. This guards against a future scan mapping that ignores the empty
// value or leaves it stale.
func TestClosedBySession_ClearedOnReopen_ni2ph(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()
	ctx, cancel := testContext(t)
	defer cancel()

	createPerm(t, ctx, store, "cbs-reopen")
	if err := store.CloseIssue(ctx, "cbs-reopen", "done", "tester", "sess-BEFORE-REOPEN"); err != nil {
		t.Fatalf("CloseIssue --session: %v", err)
	}
	if got, err := store.GetIssue(ctx, "cbs-reopen"); err != nil {
		t.Fatalf("GetIssue (pre-reopen): %v", err)
	} else if got.ClosedBySession != "sess-BEFORE-REOPEN" {
		t.Fatalf("precondition: ClosedBySession = %q, want sess-BEFORE-REOPEN", got.ClosedBySession)
	}

	if err := store.ReopenIssue(ctx, "cbs-reopen", "reopened", "tester"); err != nil {
		t.Fatalf("ReopenIssue: %v", err)
	}
	got, err := store.GetIssue(ctx, "cbs-reopen")
	if err != nil {
		t.Fatalf("GetIssue (post-reopen): %v", err)
	}
	if got.Status != types.StatusOpen {
		t.Fatalf("status = %q, want open after reopen", got.Status)
	}
	if got.ClosedBySession != "" {
		t.Errorf("ClosedBySession = %q, want empty after reopen (reopen writes '', now observable via the read fix)", got.ClosedBySession)
	}
}
