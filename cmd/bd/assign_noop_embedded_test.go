//go:build cgo

package main

import (
	"os"
	"strings"
	"testing"
)

// bdAssign runs "bd assign" and returns raw stdout (expects success rc=0).
func bdAssign(t *testing.T, bd, dir string, args ...string) string {
	t.Helper()
	return bdCommand(t, bd, dir, append([]string{"assign"}, args...)...)
}

// TestEmbeddedAssignNoOpHonest covers beads-xqsy: `bd assign <id> <same-assignee>`
// is an idempotent no-op and must report an honest "no change" rather than a false
// "✓ Assigned/Unassigned" success. Sibling of the landed bwla (dep-add no-op) /
// w2tk (dep-remove) / yaux (label-remove) false-success class. Deterministic
// (server-free once the store exists); gated on the embedded-dolt env like its
// siblings.
func TestEmbeddedAssignNoOpHonest(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "an")

	t.Run("assign_to_current_assignee_reports_no_change", func(t *testing.T) {
		iss := bdCreate(t, bd, dir, "Assign no-op A", "--type", "task")
		first := bdAssign(t, bd, dir, iss.ID, "alice")
		if !strings.Contains(first, "Assigned") {
			t.Fatalf("first assign should report Assigned: %s", first)
		}
		// Re-assign to the SAME owner: idempotent no-op — must NOT claim ✓ Assigned.
		second := bdAssign(t, bd, dir, iss.ID, "alice")
		if strings.Contains(second, "✓") && strings.Contains(second, "Assigned") {
			t.Errorf("false success: re-assigning to the current owner printed '✓ Assigned': %s", second)
		}
		if !strings.Contains(second, "no change") {
			t.Errorf("expected an 'already assigned ... no change' message on re-assign, got: %s", second)
		}
	})

	t.Run("unassign_already_unassigned_reports_no_change", func(t *testing.T) {
		iss := bdCreate(t, bd, dir, "Assign no-op B", "--type", "task")
		// A freshly-created issue is unassigned; unassigning it changes nothing.
		out := bdAssign(t, bd, dir, iss.ID, "none")
		if strings.Contains(out, "✓") && strings.Contains(out, "Unassigned") {
			t.Errorf("false success: unassigning an already-unassigned issue printed '✓ Unassigned': %s", out)
		}
		if !strings.Contains(out, "no change") {
			t.Errorf("expected an 'already unassigned, no change' message, got: %s", out)
		}
	})

	t.Run("real_reassign_still_succeeds", func(t *testing.T) {
		iss := bdCreate(t, bd, dir, "Assign no-op C", "--type", "task")
		bdAssign(t, bd, dir, iss.ID, "alice")
		// A genuine change (alice -> bob) must still report a real success.
		out := bdAssign(t, bd, dir, iss.ID, "bob")
		if !strings.Contains(out, "Assigned") || strings.Contains(out, "no change") {
			t.Errorf("real reassign should report a genuine 'Assigned', got: %s", out)
		}
	})
}
