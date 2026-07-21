//go:build cgo

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"testing"
)

// TestEmbeddedUpdateDeferPastOnDeferred is the end-to-end tooth for beads-z6jqx.
//
// This is the INVERSE residual leg of beads-jy4r9. jy4r9 fixed `bd defer <past>`
// (defer.go) to flip status→open, establishing the invariant "defer_until in the
// past ⟹ ready-visible" and asserting it in defer_past_date_embedded_test.go. But
// it left the un-covered `bd update --defer <past>` leg on an ALREADY-deferred
// issue: update.go's old `!inPast` guard only SKIPPED setting status=deferred, it
// never CLEARED a pre-existing status=deferred back to open. So the three cases
// diverged even though all three print the same "appears in bd ready immediately"
// warning:
//
//	CASE 1  update --defer <past> on an OPEN issue     -> open, ready   ✓ (truthful)
//	CASE 2  update --defer <past> on a DEFERRED issue  -> deferred, hidden ✗ (LIES) <- the bug
//	CASE 3  bd defer --until <past> on a DEFERRED issue -> open, ready   ✓ (jy4r9)
//
// The fix sets clearDeferStatus on the in-past path so a currently-deferred issue
// transitions back to open (reusing the same loop guard that flips ONLY when the
// issue is status=deferred, so in_progress/blocked are never clobbered).
func TestEmbeddedUpdateDeferPastOnDeferred(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "z6")

	statusOf := func(t *testing.T, id string) string {
		t.Helper()
		return getIssueStatus(t, bd, dir, id)
	}
	inReady := func(t *testing.T, id string) bool {
		t.Helper()
		cmd := exec.Command(bd, "ready", "--json")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		out, _, err := runCommandBuffers(t, cmd)
		if err != nil {
			t.Fatalf("bd ready --json failed: %v", err)
		}
		var issues []map[string]interface{}
		if jerr := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &issues); jerr != nil {
			t.Fatalf("parse ready JSON: %v\n%s", jerr, out.String())
		}
		for _, i := range issues {
			if i["id"] == id {
				return true
			}
		}
		return false
	}
	updateDefer := func(t *testing.T, id, date string) {
		t.Helper()
		cmd := exec.Command(bd, "update", id, "--defer", date)
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("bd update %s --defer %s failed: %v\n%s", id, date, err, out)
		}
	}

	// CASE 2 (the bug): an already-deferred issue given a PAST --defer must come
	// back to open + ready-visible, honoring the printed warning. RED before the
	// fix: status stays "deferred" and inReady is false.
	t.Run("update_defer_past_on_deferred_becomes_ready", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "z6jqx already-deferred", "--type", "task")
		// First defer it (future date) so it is genuinely status=deferred + hidden.
		updateDefer(t, issue.ID, "+8760h")
		if got := statusOf(t, issue.ID); got != "deferred" {
			t.Fatalf("precondition: future --defer should set status=deferred, got %q", got)
		}
		if inReady(t, issue.ID) {
			t.Fatalf("precondition: a future-deferred issue must be hidden from bd ready")
		}
		// Now re-defer to a PAST date: the warning claims it will appear in bd
		// ready immediately, so it must actually transition back to open.
		updateDefer(t, issue.ID, "2020-01-01")
		if got := statusOf(t, issue.ID); got != "open" {
			t.Errorf("update --defer <past> on an already-deferred issue must clear status to open, got %q", got)
		}
		if !inReady(t, issue.ID) {
			t.Errorf("update --defer <past> on an already-deferred issue must make it ready-visible (honor the printed warning)")
		}
	})

	// CASE 1 control: an OPEN issue given a PAST --defer stays open + ready
	// (unchanged behavior — the fix must not regress it).
	t.Run("update_defer_past_on_open_stays_ready", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "z6jqx open past defer", "--type", "task")
		updateDefer(t, issue.ID, "2020-01-01")
		if got := statusOf(t, issue.ID); got != "open" {
			t.Errorf("update --defer <past> on an open issue must keep status=open, got %q", got)
		}
		if !inReady(t, issue.ID) {
			t.Errorf("update --defer <past> on an open issue must stay ready-visible")
		}
	})

	// REGRESSION GUARD: a FUTURE --defer must STILL transition to deferred + hide
	// (the fix is scoped to the in-past case only).
	t.Run("update_defer_future_still_defers", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "z6jqx future defer", "--type", "task")
		updateDefer(t, issue.ID, "+24h")
		if got := statusOf(t, issue.ID); got != "deferred" {
			t.Errorf("update --defer <future> must set status=deferred (regression), got %q", got)
		}
		if inReady(t, issue.ID) {
			t.Errorf("a future-deferred issue must be hidden from bd ready (regression)")
		}
	})

	// GUARD: the clear must NOT clobber a non-deferred status. An in_progress
	// issue given a PAST --defer keeps in_progress (the loop guard flips only
	// status=deferred).
	t.Run("update_defer_past_does_not_clobber_in_progress", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "z6jqx in-progress past defer", "--type", "task")
		// Move to in_progress explicitly.
		setCmd := exec.Command(bd, "update", issue.ID, "--status", "in_progress")
		setCmd.Dir = dir
		setCmd.Env = bdEnv(dir)
		if out, err := setCmd.CombinedOutput(); err != nil {
			t.Fatalf("set in_progress failed: %v\n%s", err, out)
		}
		updateDefer(t, issue.ID, "2020-01-01")
		if got := statusOf(t, issue.ID); got != "in_progress" {
			t.Errorf("update --defer <past> must not clobber an in_progress status, got %q", got)
		}
	})

	// PARITY: bd defer <past> and bd update --defer <past> on an already-deferred
	// issue must agree (both open) — the divergence z6jqx closes.
	t.Run("agrees_with_bd_defer_past_on_deferred", func(t *testing.T) {
		a := bdCreate(t, bd, dir, "z6jqx defer-path", "--type", "task")
		b := bdCreate(t, bd, dir, "z6jqx update-path", "--type", "task")
		// Defer both (future) first.
		bdDefer(t, bd, dir, a.ID, "--until", "+8760h")
		updateDefer(t, b.ID, "+8760h")
		// Now past-defer via the two entry points.
		bdDefer(t, bd, dir, a.ID, "--until", "2019-06-01")
		updateDefer(t, b.ID, "2019-06-01")
		sa, sb := statusOf(t, a.ID), statusOf(t, b.ID)
		if sa != sb {
			t.Errorf("bd defer and bd update --defer disagree on a past date over an already-deferred issue: defer=%q update=%q (want both open)", sa, sb)
		}
		if sa != "open" {
			t.Errorf("both past-defer paths should yield status=open on an already-deferred issue, got %q", sa)
		}
	})
}
