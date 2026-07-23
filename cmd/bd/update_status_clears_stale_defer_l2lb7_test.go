//go:build cgo

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"testing"
)

// TestUpdateStatusClearsStaleDefer_l2lb7 pins the beads-l2lb7 fix: explicitly
// transitioning a FUTURE-deferred issue to a ready-visible status via
// `bd update --status open` (or --status in_progress) must clear the stale
// future defer_until, so the issue is not left status=open-but-INVISIBLE to
// `bd ready`.
//
// THE BUG (status/defer_until incoherence): `bd defer --until +24h` sets
// status=deferred AND defer_until=<future>. The ready predicate (ready.go:278)
// hides a row whenever `defer_until > UTC_TIMESTAMP()`, regardless of status.
// `bd update --status open` flipped only the status column and left the future
// defer_until intact → the issue reports status=open yet never appears in
// `bd ready`: a self-contradictory state.
//
// This is the INVERSE leg of the GH#3233 / jy4r9 / z6jqx work, which handled the
// `--defer=""` and past-`--defer` directions (clear defer_until / restore open)
// but not the "explicitly set an active status while a stale future defer_until
// lingers" direction. The fix, in update.go's per-issue apply loop, clears
// defer_until when the caller explicitly sets a ready-visible status
// (open/in_progress) and the row currently carries a future defer_until.
//
// Mutation check: remove the l2lb7 clear-stale-defer block in update.go and the
// *_becomes_ready subtests go RED (status flips to open/in_progress but the row
// stays hidden from bd ready because defer_until is still in the future).
func TestUpdateStatusClearsStaleDefer_l2lb7(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "lz")

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
	deferUntilSet := func(t *testing.T, id string) bool {
		t.Helper()
		cmd := exec.Command(bd, "show", id, "--json")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		out, _, err := runCommandBuffers(t, cmd)
		if err != nil {
			t.Fatalf("bd show --json failed: %v", err)
		}
		// bd show --json returns a single object or a one-element array.
		trimmed := bytes.TrimSpace(out.Bytes())
		var issue struct {
			DeferUntil *string `json:"defer_until"`
		}
		if len(trimmed) > 0 && trimmed[0] == '[' {
			var arr []struct {
				DeferUntil *string `json:"defer_until"`
			}
			if jerr := json.Unmarshal(trimmed, &arr); jerr != nil {
				t.Fatalf("parse show JSON array: %v\n%s", jerr, out.String())
			}
			if len(arr) == 0 {
				t.Fatalf("bd show --json returned empty array for %s", id)
			}
			return arr[0].DeferUntil != nil && *arr[0].DeferUntil != ""
		}
		if jerr := json.Unmarshal(trimmed, &issue); jerr != nil {
			t.Fatalf("parse show JSON object: %v\n%s", jerr, out.String())
		}
		return issue.DeferUntil != nil && *issue.DeferUntil != ""
	}

	deferFuture := func(t *testing.T, id string) {
		t.Helper()
		cmd := exec.Command(bd, "defer", id, "--until", "+240h")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("bd defer --until +240h failed: %v\n%s", err, out)
		}
	}
	updateStatus := func(t *testing.T, id, status string) {
		t.Helper()
		cmd := exec.Command(bd, "update", id, "--status", status)
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("bd update %s --status %s failed: %v\n%s", id, status, err, out)
		}
	}

	// (1) update --status open on a FUTURE-deferred issue must clear the stale
	// defer_until and restore ready visibility (no status=open-but-hidden).
	t.Run("status_open_clears_stale_defer_becomes_ready", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "l2lb7 open", "--type", "task")
		deferFuture(t, issue.ID)
		if got := statusOf(t, issue.ID); got != "deferred" {
			t.Fatalf("precondition: future defer should set status=deferred, got %q", got)
		}
		if inReady(t, issue.ID) {
			t.Fatalf("precondition: future-deferred issue should be hidden from bd ready")
		}
		updateStatus(t, issue.ID, "open")
		if got := statusOf(t, issue.ID); got != "open" {
			t.Errorf("status should be open after update --status open, got %q", got)
		}
		if deferUntilSet(t, issue.ID) {
			t.Errorf("stale future defer_until should be cleared by update --status open")
		}
		if !inReady(t, issue.ID) {
			t.Errorf("issue set status=open must appear in bd ready (status/defer_until incoherence bug)")
		}
	})

	// (2) same clear for in_progress. `bd ready` shows only status=open by
	// design (ready.go:178, "not in_progress"), so an in_progress issue is
	// never ready-visible regardless of defer_until — the observable effect of
	// the fix here is the cleared stale defer_until (via bd show), which is what
	// leaves the row coherent for `bd ready --include-deferred` / list --ready.
	t.Run("status_in_progress_clears_stale_defer", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "l2lb7 in_progress", "--type", "task")
		deferFuture(t, issue.ID)
		updateStatus(t, issue.ID, "in_progress")
		if got := statusOf(t, issue.ID); got != "in_progress" {
			t.Errorf("status should be in_progress, got %q", got)
		}
		if deferUntilSet(t, issue.ID) {
			t.Errorf("stale future defer_until should be cleared by update --status in_progress")
		}
	})

	// (3) REGRESSION: update --status deferred must NOT clear defer_until (a
	// deliberate re-defer keeps the schedule).
	t.Run("status_deferred_keeps_defer", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "l2lb7 redefer", "--type", "task")
		deferFuture(t, issue.ID)
		updateStatus(t, issue.ID, "deferred")
		if !deferUntilSet(t, issue.ID) {
			t.Errorf("update --status deferred must keep the future defer_until")
		}
		if inReady(t, issue.ID) {
			t.Errorf("still-deferred issue must stay hidden from bd ready")
		}
	})

	// (4) REGRESSION: an issue with NO defer_until set to open is unaffected (no
	// spurious writes / errors).
	t.Run("status_open_no_defer_unaffected", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "l2lb7 plain", "--type", "task")
		updateStatus(t, issue.ID, "in_progress")
		updateStatus(t, issue.ID, "open")
		if got := statusOf(t, issue.ID); got != "open" {
			t.Errorf("status should be open, got %q", got)
		}
		if !inReady(t, issue.ID) {
			t.Errorf("open issue with no defer must be in bd ready")
		}
	})
}
