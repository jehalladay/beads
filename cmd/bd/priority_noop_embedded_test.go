//go:build cgo

package main

import (
	"os"
	"strings"
	"testing"
	"time"
)

// bdPriority runs "bd priority" and returns raw stdout (expects success rc=0).
func bdPriority(t *testing.T, bd, dir string, args ...string) string {
	t.Helper()
	return bdCommand(t, bd, dir, append([]string{"priority"}, args...)...)
}

// TestEmbeddedPriorityNoOpHonest covers beads-helt4: `bd priority <id> <same-value>`
// is an idempotent no-op and must report an honest "no change" rather than a false
// "✓ Set priority of ... to PN" success — AND must skip the write so it neither
// bumps updated_at nor records a spurious audit event / commit. `priority` was the
// one single-field mutation verb still missing the no-op guard its siblings have
// (assign/xqsy, dep-add/bwla, dep-remove/w2tk, label-remove/yaux). This is the
// stronger skip-the-write shape, not the display-only bdy2 fix on `bd update`.
//
// The updated_at assertion is the load-bearing teeth: a spurious write (or the
// old unconditional UpdateIssue+auditIssueUpdate+commit) bumps updated_at, so an
// identical timestamp across the no-op proves the write, audit event, and commit
// were all skipped. Deterministic; gated on the embedded-dolt env like its
// siblings.
func TestEmbeddedPriorityNoOpHonest(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "pn")

	t.Run("same_priority_reports_no_change_and_does_not_bump_updated_at", func(t *testing.T) {
		// Create at P2, then set priority to 2 again — nothing changes.
		iss := bdCreate(t, bd, dir, "Priority no-op A", "--type", "task", "-p", "2")

		beforeUpdatedAt := jsonFieldOfShow(t, bd, dir, iss.ID, "updated_at")
		if beforeUpdatedAt == "" {
			t.Fatalf("could not read initial updated_at for %s", iss.ID)
		}

		// Ensure wall-clock advances so a real write would produce a distinct
		// timestamp (the whole point of the assertion).
		time.Sleep(1100 * time.Millisecond)

		out := bdPriority(t, bd, dir, iss.ID, "2")
		if strings.Contains(out, "✓") && strings.Contains(out, "Set priority") {
			t.Errorf("false success: setting priority to its current value printed '✓ Set priority': %s", out)
		}
		if !strings.Contains(out, "no change") {
			t.Errorf("expected an 'already P2 (no change)' message on a same-priority set, got: %s", out)
		}

		afterUpdatedAt := jsonFieldOfShow(t, bd, dir, iss.ID, "updated_at")
		if afterUpdatedAt != beforeUpdatedAt {
			t.Errorf("no-op priority bumped updated_at (spurious write/audit/commit, beads-helt4): before=%q after=%q", beforeUpdatedAt, afterUpdatedAt)
		}

		// The priority value itself must be untouched.
		got := bdShow(t, bd, dir, iss.ID)
		if got.Priority != 2 {
			t.Errorf("no-op priority changed the value: want 2, got %d", got.Priority)
		}
	})

	t.Run("real_change_still_succeeds_and_bumps_updated_at", func(t *testing.T) {
		iss := bdCreate(t, bd, dir, "Priority no-op B", "--type", "task", "-p", "2")

		beforeUpdatedAt := jsonFieldOfShow(t, bd, dir, iss.ID, "updated_at")
		time.Sleep(1100 * time.Millisecond)

		// A genuine change (2 -> 0) must still report a real success and write.
		out := bdPriority(t, bd, dir, iss.ID, "0")
		if !strings.Contains(out, "Set priority") || strings.Contains(out, "no change") {
			t.Errorf("real priority change should report a genuine 'Set priority', got: %s", out)
		}

		got := bdShow(t, bd, dir, iss.ID)
		if got.Priority != 0 {
			t.Errorf("real priority change did not persist: want 0, got %d", got.Priority)
		}
		afterUpdatedAt := jsonFieldOfShow(t, bd, dir, iss.ID, "updated_at")
		if afterUpdatedAt == beforeUpdatedAt {
			t.Errorf("real priority change did NOT bump updated_at: before==after==%q", beforeUpdatedAt)
		}
	})
}
