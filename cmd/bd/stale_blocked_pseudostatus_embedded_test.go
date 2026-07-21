//go:build cgo

package main

import (
	"os"
	"strings"
	"testing"
)

// TestEmbeddedStaleStatusBlocked covers beads-h40fl: `bd stale --status blocked`
// must surface stale BLOCKED issues instead of silently returning zero.
//
// "blocked" is a derived pseudo-status — a blocked issue keeps its stored
// status as 'open'/'in_progress' and blocked-ness lives in the denormalized
// is_blocked column. The stale path historically filtered on the stored status
// column (`status = 'blocked'`, unsatisfiable by construction), so a genuinely
// stale blocked issue produced a clean rc=0 "No stale issues found" false
// negative. The fix routes --status blocked to the is_blocked predicate,
// mirroring beads-7f3g for bd list/count and bd blocked's closed/pinned
// exclusion. This is the end-to-end teeth (the sqlmock builder test pins the
// WHERE clause; this proves the observable command behavior).
func TestEmbeddedStaleStatusBlocked(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, beadsDir, _ := bdInit(t, bd, "--prefix", "sb")

	// Chain: DEP depends-on BLK  → DEP is_blocked=1 (stored status stays 'open').
	blk := bdCreate(t, bd, dir, "root blocker", "--type", "task")
	dep := bdCreate(t, bd, dir, "blocked dependent", "--type", "task")
	bdDep(t, bd, dir, "add", dep.ID, blk.ID, "--type", "blocks")

	// A stale, non-blocked control that must NOT appear under --status blocked.
	plain := bdCreate(t, bd, dir, "stale but not blocked", "--type", "task")

	// Backdate updated_at AFTER wiring the dep (dep add bumps updated_at).
	makeIssuesStale(t, beadsDir, "sb", []string{dep.ID, plain.ID})

	// Sanity: the dependent's STORED status is not literally 'blocked' (it's the
	// is_blocked pseudo-status). If it were, `status = 'blocked'` would have
	// matched and there'd be no bug.
	show := bdShowJSON(t, bd, dir, dep.ID)
	if strings.Contains(show, `"status": "blocked"`) || strings.Contains(show, `"status":"blocked"`) {
		t.Fatalf("precondition: stored status should not literally be 'blocked':\n%s", show)
	}

	t.Run("stale_blocked_surfaces_dependent", func(t *testing.T) {
		entries := bdStaleJSON(t, bd, dir, "--status", "blocked", "--days", "1")
		if !staleContainsID(entries, dep.ID) {
			t.Errorf("bd stale --status blocked should list the stale blocked dependent %s (beads-h40fl); got %d entries: %v",
				dep.ID, len(entries), staleIDsOf(entries))
		}
		// The blocker itself is not blocked; the plain issue is not blocked.
		if staleContainsID(entries, blk.ID) {
			t.Errorf("blocker %s is not blocked and must not appear under --status blocked", blk.ID)
		}
		if staleContainsID(entries, plain.ID) {
			t.Errorf("non-blocked stale issue %s must not appear under --status blocked", plain.ID)
		}
	})

	// Parity guard against bd list --status blocked (the 7f3g authority): the
	// stale-blocked dependent is both blocked AND stale, so it must appear in
	// both the list-blocked view and the stale-blocked view.
	t.Run("agrees_with_list_blocked", func(t *testing.T) {
		listed := bdListJSON(t, bd, dir, "--status", "blocked")
		if !containsID(listed, dep.ID) {
			t.Fatalf("precondition: bd list --status blocked should include %s", dep.ID)
		}
		entries := bdStaleJSON(t, bd, dir, "--status", "blocked", "--days", "1")
		if !staleContainsID(entries, dep.ID) {
			t.Errorf("stale --status blocked disagrees with list --status blocked for the stale blocked issue %s", dep.ID)
		}
	})
}

func staleContainsID(entries []map[string]interface{}, id string) bool {
	for _, e := range entries {
		if e["id"] == id {
			return true
		}
	}
	return false
}

func staleIDsOf(entries []map[string]interface{}) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if s, ok := e["id"].(string); ok {
			out = append(out, s)
		}
	}
	return out
}
