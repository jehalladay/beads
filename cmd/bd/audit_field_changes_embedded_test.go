//go:build cgo

package main

import (
	"os"
	"testing"
)

// beads-n4sn (class): the single-field commands `bd assign` and `bd priority`
// change audited fields (assignee/priority) just like `bd update`, so they must
// write the GC-survivable audit-file trail via the shared auditIssueUpdate
// chokepoint. Before the fix they emitted no trail — the same scattered-emission
// gap that hid reopen/defer.
func TestEmbeddedAssignPriorityWriteAuditTrail(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "au")

	t.Run("assign_writes_audit_trail", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "Assign audit", "--type", "task")
		bdCommand(t, bd, dir, "assign", issue.ID, "alice")

		if !auditHasFieldChange(t, dir, issue.ID, "assignee", "alice") {
			t.Errorf("bd assign did not write a GC-survivable audit field_change for assignee (beads-n4sn)")
		}
	})

	t.Run("priority_writes_audit_trail", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "Priority audit", "--type", "task")
		bdCommand(t, bd, dir, "priority", issue.ID, "0")

		if !auditHasFieldChange(t, dir, issue.ID, "priority", "0") {
			t.Errorf("bd priority did not write a GC-survivable audit field_change for priority (beads-n4sn)")
		}
	})
}
