//go:build cgo

package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestEmbeddedLintStatusBlocked is the teeth for beads-pbelp: `bd lint --status
// blocked` must lint the BLOCKED cohort, not silently check zero.
//
// "blocked" is a derived pseudo-status (the is_blocked column, beads-7f3g), not
// a stored status value. lint set filter.Status = "blocked", which builds
// `status = 'blocked'` — a predicate that matches nothing — so `bd lint
// --status blocked` reported "0 issues checked" (rc0) regardless of how many
// blocked issues exist, a false "clean" that never surfaces template/structural
// warnings on blocked issues. --status blocked is ACCEPTED (validates via
// de4r/8cg2), so it reads as "no blocked issues" rather than "this filter can't
// match". Sibling of beads-h40fl (bd stale --status blocked).
//
// The fix translates the blocked pseudo-status to filter.Blocked (routing to
// is_blocked=1, beads-7f3g), exactly as bd count / bd list do. The teeth create
// a blocked issue that also has a lint warning (a bare bug missing its
// template) and assert lint checks it under --status blocked.
func TestEmbeddedLintStatusBlocked(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)

	depAdd := func(t *testing.T, dir, blocked, blocker string) {
		t.Helper()
		cmd := exec.Command(bd, "dep", "add", blocked, blocker, "--type", "blocks")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("dep add %s %s failed: %v\n%s", blocked, blocker, err, out)
		}
	}

	// The core integrity leg: a blocked issue with a lint warning must be
	// CHECKED (and its warning surfaced) under --status blocked.
	t.Run("checks_blocked_issue_with_warning", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "lsb")
		// A bare bug (no Steps to Reproduce / Acceptance Criteria) -> lint warns.
		blockedBug := bdCreate(t, bd, dir, "Blocked bare bug", "--type", "bug",
			"--description", "Something is broken")
		blocker := bdCreate(t, bd, dir, "Blocker", "--type", "task")
		depAdd(t, dir, blockedBug.ID, blocker.ID) // blockedBug is_blocked=1, stored status stays 'open'

		m := bdLintJSON(t, bd, dir, "--status", "blocked")
		// The blocked bare bug must appear in the results (checked + warned).
		results, _ := m["results"].([]interface{})
		found := false
		for _, r := range results {
			rm, _ := r.(map[string]interface{})
			if rm["id"] == blockedBug.ID {
				found = true
			}
		}
		if !found {
			t.Errorf("bd lint --status blocked must CHECK the blocked bare bug %s and surface its warning (beads-pbelp: RED today = 0 issues checked); results=%v", blockedBug.ID, results)
		}

		// Text mode: it must NOT report "0 issues checked" while a lintable
		// blocked issue exists (the false-clean the bead documents).
		out, _ := bdLint(t, bd, dir, "--status", "blocked")
		if strings.Contains(out, "0 issues checked") {
			t.Errorf("bd lint --status blocked reported '0 issues checked' with a blocked issue present (beads-pbelp); got:\n%s", out)
		}
	})

	// The blocked cohort lint scans must match bd list --status blocked (parity),
	// and a real stored status (open) is unaffected.
	t.Run("blocked_scope_agrees_with_list_and_open_unaffected", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "lsc")
		blockedBug := bdCreate(t, bd, dir, "Blocked bug 2", "--type", "bug",
			"--description", "broken")
		blocker := bdCreate(t, bd, dir, "Blocker 2", "--type", "task")
		depAdd(t, dir, blockedBug.ID, blocker.ID)
		// An unblocked bare bug: shows under --status open, NOT under blocked.
		openBug := bdCreate(t, bd, dir, "Open bug", "--type", "bug", "--description", "broken")

		// list --status blocked returns the blocked bug (via 7f3g), not the open one.
		listCmd := exec.Command(bd, "list", "--status", "blocked", "--json")
		listCmd.Dir = dir
		listCmd.Env = bdEnv(dir)
		listOut, err := listCmd.CombinedOutput()
		if err != nil {
			t.Fatalf("bd list --status blocked failed: %v\n%s", err, listOut)
		}
		ls := string(listOut)
		if !strings.Contains(ls, blockedBug.ID) {
			t.Fatalf("setup: bd list --status blocked should contain %s; got:\n%s", blockedBug.ID, ls)
		}

		// lint --status blocked checks the blocked bug and NOT the open one.
		m := bdLintJSON(t, bd, dir, "--status", "blocked")
		results, _ := m["results"].([]interface{})
		var ids []string
		for _, r := range results {
			rm, _ := r.(map[string]interface{})
			if id, ok := rm["id"].(string); ok {
				ids = append(ids, id)
			}
		}
		joined := strings.Join(ids, ",")
		if !strings.Contains(joined, blockedBug.ID) {
			t.Errorf("lint --status blocked should check the blocked bug %s; got ids=%v", blockedBug.ID, ids)
		}
		if strings.Contains(joined, openBug.ID) {
			t.Errorf("lint --status blocked must NOT check the unblocked open bug %s; got ids=%v", openBug.ID, ids)
		}

		// The open scope still works (real stored status unaffected by the fix).
		mo := bdLintJSON(t, bd, dir, "--status", "open")
		or, _ := mo["results"].([]interface{})
		openFound := false
		for _, r := range or {
			rm, _ := r.(map[string]interface{})
			if rm["id"] == openBug.ID {
				openFound = true
			}
		}
		if !openFound {
			t.Errorf("bd lint --status open must still check the open bare bug %s (fix must not disturb real statuses)", openBug.ID)
		}
	})

	// A formerly-blocked issue that is now CLOSED must NOT be checked under
	// --status blocked — matching the 7f3g predicate's closed/pinned exclusion.
	t.Run("excludes_closed_from_blocked_scope", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "lsd")
		blockedBug := bdCreate(t, bd, dir, "Blocked then closed", "--type", "bug",
			"--description", "broken")
		blocker := bdCreate(t, bd, dir, "Blocker 3", "--type", "task")
		depAdd(t, dir, blockedBug.ID, blocker.ID)
		// --force: the zgku close guard refuses to close an issue blocked by open
		// issues; force past it (this is exactly the closed-but-formerly-blocked
		// state the 7f3g exclusion must drop from the blocked scope).
		bdClose(t, bd, dir, blockedBug.ID, "--force")

		m := bdLintJSON(t, bd, dir, "--status", "blocked")
		results, _ := m["results"].([]interface{})
		for _, r := range results {
			rm, _ := r.(map[string]interface{})
			if rm["id"] == blockedBug.ID {
				t.Errorf("bd lint --status blocked must EXCLUDE the closed issue %s (7f3g closed/pinned exclusion)", blockedBug.ID)
			}
		}
	})

	// --status blocked stays a VALID value (de4r/8cg2): no "invalid status"
	// error, and the command exits cleanly when nothing lintable is blocked.
	t.Run("blocked_is_valid_status_value", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "lse")
		bdCreate(t, bd, dir, "lonely open", "--type", "task")

		out, rc := bdLint(t, bd, dir, "--status", "blocked")
		if strings.Contains(out, "invalid status") {
			t.Errorf("--status blocked must remain a VALID lint status (de4r/8cg2); got:\n%s", out)
		}
		// No blocked issues -> clean pass, rc0 (no warnings, no error).
		if rc != 0 {
			t.Errorf("bd lint --status blocked with no blocked issues should exit 0; got rc=%d\n%s", rc, out)
		}
	})
}
