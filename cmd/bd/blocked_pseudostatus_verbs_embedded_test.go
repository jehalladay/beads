//go:build cgo

package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestEmbeddedBlockedPseudoStatusVerbs covers beads-3x0e4: the "blocked"
// pseudo-status must route to the is_blocked predicate on the remaining verbs
// that accept --status and feed filter.Status → SearchIssues, mirroring the
// beads-7f3g (bd list/count) and beads-h40fl (bd stale) fixes.
//
// "blocked" is a DERIVED pseudo-status: a blocked issue keeps its stored status
// as 'open'/'in_progress' and blocked-ness lives in the denormalized is_blocked
// column. A plain `status = 'blocked'` WHERE clause is unsatisfiable by
// construction, so before the fix these verbs silently returned 0 results with
// rc=0 — a false negative:
//   - bd find-duplicates (direct + proxied): filter.Status = &blocked
//   - bd human list:                          filter.Status = &blocked
//   - bd search (single --status blocked):    filter.Status = &blocked
//   - bd search (multi --status a,blocked):    filter.Statuses IN(...,blocked)
//
// The single-value fix routes StatusBlocked → filter.Blocked (is_blocked
// predicate). The multi-value search leg REJECTS blocked explicitly (a derived
// status cannot be OR-combined in a status-column IN() filter), matching the
// landed list_filter.go/count.go precedent.
func TestEmbeddedBlockedPseudoStatusVerbs(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "bps")

	// Two near-identical blocked bugs (so find-duplicates has a pair) + a shared
	// blocker. Wiring the blocks edge makes both dependents is_blocked=1 while
	// their stored status stays 'open'.
	blk := bdCreate(t, bd, dir, "root blocker task", "--type", "task")
	dupA := bdCreate(t, bd, dir, "Fix login page timeout error", "--type", "bug",
		"--description", "The login page throws a timeout error after 30 seconds of inactivity")
	dupB := bdCreate(t, bd, dir, "Login page timeout error fix needed", "--type", "bug",
		"--description", "After 30 seconds the login page shows a timeout error to the user")
	bdDep(t, bd, dir, "add", dupA.ID, blk.ID, "--type", "blocks")
	bdDep(t, bd, dir, "add", dupB.ID, blk.ID, "--type", "blocks")

	// Precondition: bd list --status blocked (the 7f3g authority) sees both
	// dependents — proves is_blocked=1, so a working --status blocked on the
	// other verbs must see them too. The blocker is not blocked.
	t.Run("precondition_list_blocked_sees_dependents", func(t *testing.T) {
		listed := bdListJSON(t, bd, dir, "--status", "blocked")
		if !containsID(listed, dupA.ID) || !containsID(listed, dupB.ID) {
			t.Fatalf("precondition: bd list --status blocked should include %s and %s", dupA.ID, dupB.ID)
		}
		if containsID(listed, blk.ID) {
			t.Fatalf("precondition: blocker %s is not blocked and must not appear", blk.ID)
		}
	})

	// ===== bd find-duplicates --status blocked (direct path) =====
	// Both duplicates are blocked, so restricting to blocked keeps the pair.
	t.Run("find_duplicates_status_blocked_finds_pair", func(t *testing.T) {
		m := bdFindDupsJSON(t, bd, dir, "--status", "blocked", "--threshold", "0.15")
		pairs, ok := m["pairs"].([]interface{})
		if !ok {
			t.Fatalf("expected pairs array, got %T", m["pairs"])
		}
		if len(pairs) == 0 {
			t.Errorf("beads-3x0e4: find-duplicates --status blocked returned 0 pairs; " +
				"both duplicates are is_blocked=1 so the blocked pair must survive " +
				"(status='blocked' is unsatisfiable → false negative before the fix)")
		}
	})

	// ===== bd search --status blocked (single value, search.go) =====
	t.Run("search_status_blocked_single", func(t *testing.T) {
		results := bdSearchJSON(t, bd, dir, "--status", "blocked", "timeout")
		if !searchResultsContain(results, dupA.ID) && !searchResultsContain(results, dupB.ID) {
			t.Errorf("beads-3x0e4: bd search --status blocked returned no blocked matches "+
				"(want %s or %s); status='blocked' clause is unsatisfiable before the fix: %v",
				dupA.ID, dupB.ID, searchIDsOf(results))
		}
		// The blocker is not blocked and is not a timeout match — must not appear.
		if searchResultsContain(results, blk.ID) {
			t.Errorf("non-blocked blocker %s must not appear under --status blocked", blk.ID)
		}
	})

	// ===== bd search --status open,blocked (multi value) is REJECTED =====
	// A derived status cannot be OR-combined in a status-column IN() filter.
	// Mirrors the landed list_filter.go/count.go precedent (reject, don't 0-out).
	t.Run("search_status_multi_with_blocked_rejected", func(t *testing.T) {
		out := bdSearchFail(t, bd, dir, "--status", "open,blocked", "timeout")
		if !strings.Contains(out, "derived and cannot be combined") {
			t.Errorf("beads-3x0e4: bd search --status open,blocked should reject the derived "+
				"status explicitly, got: %s", out)
		}
	})

	// ===== bd human list --status blocked =====
	// A blocked issue carrying the 'human' label must surface under --status
	// blocked (not silently drop to the empty "No human-needed beads" result).
	t.Run("human_list_status_blocked", func(t *testing.T) {
		hblk := bdCreate(t, bd, dir, "human blocker", "--type", "task")
		hdep := bdCreate(t, bd, dir, "needs human input while blocked", "--type", "task",
			"--label", "human")
		bdDep(t, bd, dir, "add", hdep.ID, hblk.ID, "--type", "blocks")

		out := bdHuman(t, bd, dir, "list", "--status", "blocked")
		if !strings.Contains(out, hdep.ID) {
			t.Errorf("beads-3x0e4: bd human list --status blocked should list the blocked "+
				"human-labeled issue %s; status='blocked' is unsatisfiable before the fix:\n%s",
				hdep.ID, out)
		}
	})
}

// bdSearchFail runs "bd search" expecting a non-zero exit; returns combined output.
func bdSearchFail(t *testing.T, bd, dir string, args ...string) string {
	t.Helper()
	fullArgs := append([]string{"search"}, args...)
	cmd := exec.Command(bd, fullArgs...)
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected bd search %s to fail, but succeeded:\n%s", strings.Join(args, " "), out)
	}
	return string(out)
}

func searchResultsContain(results []map[string]interface{}, id string) bool {
	for _, r := range results {
		if r["id"] == id {
			return true
		}
	}
	return false
}

func searchIDsOf(results []map[string]interface{}) []string {
	out := make([]string, 0, len(results))
	for _, r := range results {
		if s, ok := r["id"].(string); ok {
			out = append(out, s)
		}
	}
	return out
}
