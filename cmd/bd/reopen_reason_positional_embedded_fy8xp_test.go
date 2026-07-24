//go:build cgo

package main

import (
	"os"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestEmbeddedReopenReasonPositional_fy8xp proves `bd reopen` maps a repeated
// --reason POSITIONALLY across multiple IDs, matching `bd close`/`bd defer`.
//
// Before the fix reopen read a single GetString("reason") (cobra last-wins), so
// `bd reopen A B --reason r1 --reason r2` dropped r1 and recorded r2 on BOTH A
// and B — silent batch data loss. reopen persists its reason on the
// GC-survivable audit-file field_change (status→open), so this drives the real
// embedded store end to end and asserts each issue's status-change entry got
// ITS OWN reason (r1 on A only, r2 on B only), a single --reason still
// broadcasts, and a count that is neither 1 nor N is rejected before any write.
func TestEmbeddedReopenReasonPositional_fy8xp(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "rp")

	// The core defect: N reasons for N IDs map one-per-ID, not last-wins-broadcast.
	t.Run("reasons_map_positionally", func(t *testing.T) {
		a := bdCreate(t, bd, dir, "positional A", "--type", "task")
		b := bdCreate(t, bd, dir, "positional B", "--type", "task")
		bdClose(t, bd, dir, a.ID)
		bdClose(t, bd, dir, b.ID)

		bdReopen(t, bd, dir, a.ID, b.ID, "-r", "reason-for-A", "-r", "reason-for-B")

		reasonA, okA := auditFieldChangeReason(t, dir, a.ID, "status", "open")
		reasonB, okB := auditFieldChangeReason(t, dir, b.ID, "status", "open")
		if !okA {
			t.Fatalf("no status→open audit entry for A")
		}
		if !okB {
			t.Fatalf("no status→open audit entry for B")
		}
		if reasonA != "reason-for-A" {
			t.Errorf("REGRESSION (beads-fy8xp): A got reason %q, want %q (broadcast last-wins would give %q)", reasonA, "reason-for-A", "reason-for-B")
		}
		if reasonB != "reason-for-B" {
			t.Errorf("REGRESSION (beads-fy8xp): B got reason %q, want %q", reasonB, "reason-for-B")
		}
	})

	// A single --reason still broadcasts to every ID (backward-compatible).
	t.Run("single_reason_broadcasts", func(t *testing.T) {
		a := bdCreate(t, bd, dir, "broadcast A", "--type", "task")
		b := bdCreate(t, bd, dir, "broadcast B", "--type", "task")
		bdClose(t, bd, dir, a.ID)
		bdClose(t, bd, dir, b.ID)

		bdReopen(t, bd, dir, a.ID, b.ID, "--reason", "shared-reason")

		reasonA, okA := auditFieldChangeReason(t, dir, a.ID, "status", "open")
		reasonB, okB := auditFieldChangeReason(t, dir, b.ID, "status", "open")
		if !okA || reasonA != "shared-reason" {
			t.Errorf("single --reason did not broadcast to A; got %q (found=%v)", reasonA, okA)
		}
		if !okB || reasonB != "shared-reason" {
			t.Errorf("single --reason did not broadcast to B; got %q (found=%v)", reasonB, okB)
		}
	})

	// A reason count that is neither 1 nor len(IDs) is rejected, not guessed.
	t.Run("count_mismatch_rejected", func(t *testing.T) {
		a := bdCreate(t, bd, dir, "mismatch A", "--type", "task")
		b := bdCreate(t, bd, dir, "mismatch B", "--type", "task")
		bdClose(t, bd, dir, a.ID)
		bdClose(t, bd, dir, b.ID)

		out := bdReopenFail(t, bd, dir, a.ID, b.ID, "-r", "r1", "-r", "r2", "-r", "r3")
		if !strings.Contains(out, "3 reopen reasons for 2 issue IDs") {
			t.Errorf("count-mismatch error missing the descriptive message; out=%q", out)
		}
		// Neither issue should have been reopened (validation runs before writes).
		if got := bdShow(t, bd, dir, a.ID); got.Status != types.StatusClosed {
			t.Errorf("A was reopened despite the count-mismatch error, status=%s", got.Status)
		}
		if got := bdShow(t, bd, dir, b.ID); got.Status != types.StatusClosed {
			t.Errorf("B was reopened despite the count-mismatch error, status=%s", got.Status)
		}
	})
}
