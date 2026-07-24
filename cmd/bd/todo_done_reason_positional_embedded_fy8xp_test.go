//go:build cgo

package main

import (
	"os"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// bdTodoDone runs `bd todo done <args>`, retrying on flock contention, and
// fatals on failure. Returns stdout. (bdTodoDoneFail lives in
// todo_done_preclose_guards_k96re_test.go — reused here for the count-mismatch
// negative path.)
func bdTodoDone(t *testing.T, bd, dir string, args ...string) string {
	t.Helper()
	fullArgs := append([]string{"todo", "done"}, args...)
	out, err := bdRunWithFlockRetry(t, bd, dir, fullArgs...)
	if err != nil {
		t.Fatalf("bd todo done %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// TestEmbeddedTodoDoneReasonPositional_fy8xp proves `bd todo done` maps a
// repeated --reason POSITIONALLY across multiple IDs, matching `bd close`.
//
// Before the fix `bd todo done` read a single GetString("reason") (cobra
// last-wins), so `bd todo done A B --reason r1 --reason r2` dropped r1 and
// stored r2 as the close_reason on BOTH A and B — silent batch data loss. todo
// done stores its reason as the issue's CloseReason (it wraps bd close), so
// this drives the real embedded store end to end and asserts each issue's
// CloseReason got ITS OWN reason (r1 on A only, r2 on B only), a single
// --reason still broadcasts, no --reason falls through to the "Completed"
// default (07sko), and a count that is neither 1 nor N is rejected before any
// write.
func TestEmbeddedTodoDoneReasonPositional_fy8xp(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "td")

	// The core defect: N reasons for N IDs map one-per-ID, not last-wins-broadcast.
	t.Run("reasons_map_positionally", func(t *testing.T) {
		a := bdCreate(t, bd, dir, "positional A", "--type", "task")
		b := bdCreate(t, bd, dir, "positional B", "--type", "task")

		bdTodoDone(t, bd, dir, a.ID, b.ID, "-r", "reason-for-A", "-r", "reason-for-B")

		gotA := bdShow(t, bd, dir, a.ID)
		gotB := bdShow(t, bd, dir, b.ID)
		if gotA.Status != types.StatusClosed || gotB.Status != types.StatusClosed {
			t.Fatalf("expected both closed, got A=%s B=%s", gotA.Status, gotB.Status)
		}
		if gotA.CloseReason != "reason-for-A" {
			t.Errorf("REGRESSION (beads-fy8xp): A close_reason %q, want %q (broadcast last-wins would give %q)", gotA.CloseReason, "reason-for-A", "reason-for-B")
		}
		if gotB.CloseReason != "reason-for-B" {
			t.Errorf("REGRESSION (beads-fy8xp): B close_reason %q, want %q", gotB.CloseReason, "reason-for-B")
		}
	})

	// A single --reason still broadcasts to every ID (backward-compatible).
	t.Run("single_reason_broadcasts", func(t *testing.T) {
		a := bdCreate(t, bd, dir, "broadcast A", "--type", "task")
		b := bdCreate(t, bd, dir, "broadcast B", "--type", "task")

		bdTodoDone(t, bd, dir, a.ID, b.ID, "--reason", "shared-reason")

		if got := bdShow(t, bd, dir, a.ID); got.CloseReason != "shared-reason" {
			t.Errorf("single --reason did not broadcast to A; close_reason %q", got.CloseReason)
		}
		if got := bdShow(t, bd, dir, b.ID); got.CloseReason != "shared-reason" {
			t.Errorf("single --reason did not broadcast to B; close_reason %q", got.CloseReason)
		}
	})

	// No --reason falls through to the "Completed" default per-ID (07sko preserved).
	t.Run("no_reason_defaults_completed", func(t *testing.T) {
		a := bdCreate(t, bd, dir, "default A", "--type", "task")
		b := bdCreate(t, bd, dir, "default B", "--type", "task")

		bdTodoDone(t, bd, dir, a.ID, b.ID)

		if got := bdShow(t, bd, dir, a.ID); got.CloseReason != "Completed" {
			t.Errorf("no --reason: A close_reason %q, want Completed (07sko)", got.CloseReason)
		}
		if got := bdShow(t, bd, dir, b.ID); got.CloseReason != "Completed" {
			t.Errorf("no --reason: B close_reason %q, want Completed (07sko)", got.CloseReason)
		}
	})

	// A reason count that is neither 1 nor len(IDs) is rejected, not guessed.
	t.Run("count_mismatch_rejected", func(t *testing.T) {
		a := bdCreate(t, bd, dir, "mismatch A", "--type", "task")
		b := bdCreate(t, bd, dir, "mismatch B", "--type", "task")

		out := bdTodoDoneFail(t, bd, dir, a.ID, b.ID, "-r", "r1", "-r", "r2", "-r", "r3")
		if !strings.Contains(out, "3 close reasons for 2 issue IDs") {
			t.Errorf("count-mismatch error missing the descriptive message; out=%q", out)
		}
		// Neither issue should have been closed (validation runs before writes).
		if got := bdShow(t, bd, dir, a.ID); got.Status != types.StatusOpen {
			t.Errorf("A was closed despite the count-mismatch error, status=%s", got.Status)
		}
		if got := bdShow(t, bd, dir, b.ID); got.Status != types.StatusOpen {
			t.Errorf("B was closed despite the count-mismatch error, status=%s", got.Status)
		}
	})
}
