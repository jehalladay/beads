//go:build cgo

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestEmbeddedDeferPastDate is the end-to-end tooth for beads-jy4r9.
//
// Leg A (status divergence): `bd defer --until <past>` used to force
// status=deferred UNCONDITIONALLY (defer.go), producing a self-contradictory
// "deferred but appears in bd ready immediately" state — because the ready
// predicate ignores a past defer_until. `bd update --defer <past>` deliberately
// keeps status=open (update.go's !inPast guard). This asserts `bd defer` now
// agrees: a PAST --until keeps status=open, while a dateless defer and a FUTURE
// --until still transition to deferred (the critical regression guard the naive
// "gate the status assignment on !inPast" fix would trip).
//
// Leg B (dropped past-date warning under --json): the "appears in bd ready
// immediately" hint was suppressed under --json and surfaced nowhere. This
// asserts it now emits as a parseable JSON object on STDERR (stdout stays the
// pure issue-array success payload).
func TestEmbeddedDeferPastDate(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "dp")

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

	// (1) PAST --until -> status stays OPEN and the issue is still ready-visible
	// (matches update --defer <past>; the leg-A fix).
	t.Run("past_until_keeps_status_open_and_ready", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "jy4r9 past defer", "--type", "task")
		bdDefer(t, bd, dir, issue.ID, "--until", "2020-01-01")
		if got := statusOf(t, issue.ID); got != "open" {
			t.Errorf("past `bd defer --until` must keep status=open (not deferred-but-ready), got %q", got)
		}
		if !inReady(t, issue.ID) {
			t.Errorf("past-deferred issue must still appear in bd ready (defer_until in the past)")
		}
	})

	// (2) FUTURE --until -> status deferred (unchanged).
	t.Run("future_until_defers", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "jy4r9 future defer", "--type", "task")
		bdDefer(t, bd, dir, issue.ID, "--until", "+24h")
		if got := statusOf(t, issue.ID); got != "deferred" {
			t.Errorf("future `bd defer --until` must set status=deferred, got %q", got)
		}
	})

	// (3) REGRESSION GUARD: dateless `bd defer` (no --until) must STILL defer
	// unconditionally — the fix is scoped to the past-DATE case only.
	t.Run("dateless_defer_still_defers", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "jy4r9 dateless defer", "--type", "task")
		bdDefer(t, bd, dir, issue.ID)
		if got := statusOf(t, issue.ID); got != "deferred" {
			t.Errorf("dateless `bd defer` must keep status=deferred (regression), got %q", got)
		}
	})

	// (4) Parity with update --defer <past>: both entry points agree on open.
	t.Run("agrees_with_update_defer_past", func(t *testing.T) {
		a := bdCreate(t, bd, dir, "jy4r9 defer path", "--type", "task")
		b := bdCreate(t, bd, dir, "jy4r9 update path", "--type", "task")
		bdDefer(t, bd, dir, a.ID, "--until", "2019-06-01")
		upd := exec.Command(bd, "update", b.ID, "--defer", "2019-06-01")
		upd.Dir = dir
		upd.Env = bdEnv(dir)
		if out, err := upd.CombinedOutput(); err != nil {
			t.Fatalf("update --defer failed: %v\n%s", err, out)
		}
		sa, sb := statusOf(t, a.ID), statusOf(t, b.ID)
		if sa != sb {
			t.Errorf("bd defer and bd update --defer disagree on a past date: defer=%q update=%q (want both open)", sa, sb)
		}
		if sa != "open" {
			t.Errorf("both past-defer paths should yield status=open, got %q", sa)
		}
	})

	// Leg B: past-date --json emits the in-past signal as a JSON object on
	// STDERR, and stdout stays a pure parseable issue array.
	t.Run("past_until_json_surfaces_warning_on_stderr", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "jy4r9 json past defer", "--type", "task")
		cmd := exec.Command(bd, "defer", issue.ID, "--until", "2020-01-01", "--json")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		stdout, stderr, err := runCommandBuffers(t, cmd)
		if err != nil {
			t.Fatalf("bd defer --until <past> --json failed: %v\nstderr:\n%s", err, stderr.String())
		}
		// stdout must be a pure parseable issue array (success payload).
		var issues []map[string]interface{}
		if jerr := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &issues); jerr != nil {
			t.Fatalf("stdout is not a parseable issue array under --json: %v\nstdout:\n%s", jerr, stdout.String())
		}
		// stderr must carry the in-past signal as a JSON object (not dropped, not
		// interleaved plain text) — a --json consumer can detect the no-op defer.
		se := strings.TrimSpace(stderr.String())
		if se == "" {
			t.Fatalf("past-date --json defer dropped the 'appears in bd ready immediately' signal entirely (beads-jy4r9 leg B)")
		}
		var warn map[string]interface{}
		if jerr := json.Unmarshal([]byte(se), &warn); jerr != nil {
			t.Fatalf("past-date --json warning must be a JSON object on stderr, got non-JSON: %v\nstderr:\n%s", jerr, se)
		}
		msg, _ := warn["error"].(string)
		if data, ok := warn["data"].(map[string]interface{}); ok && msg == "" {
			msg, _ = data["error"].(string)
		}
		if !strings.Contains(strings.ToLower(msg), "past") {
			t.Errorf("expected the in-past warning message on stderr, got %q", msg)
		}
	})
}
