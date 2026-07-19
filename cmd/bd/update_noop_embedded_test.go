//go:build cgo

package main

import (
	"os"
	"strings"
	"testing"
)

// beads-bdy2: `bd update <id> --status open` on an already-open issue (and the
// other scalar fields --priority/--title/--assignee set to their current value)
// printed "✓ Updated issue" rc=0 though nothing changed — a false-success a
// CI/agent gate reads as proof of a change (the xqsy/dr3/b0tw no-op class on the
// general update path).
//
// The fix is DISPLAY-ONLY (per the bdy2 ruling): the UpdateIssue call, audit
// trail, and mutation tracking are untouched (the write stays idempotent); only
// the feedback line is honest. The guard defaults to "✓ Updated" for ANY
// non-scalar/audit-bearing flag (notes/append-notes/labels/parent/metadata/…)
// so it can never suppress a real mutation.
func TestEmbeddedUpdateNoOpHonest(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "up")

	t.Run("scalar_only_same_value_reports_no_change", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "Noop status", "--type", "task")
		// A fresh issue is status=open, so --status open is a scalar-only no-op.
		out := bdCommand(t, bd, dir, "update", issue.ID, "--status", "open")
		if strings.Contains(out, "Updated issue") {
			t.Errorf("re-setting --status to the current value must NOT print '✓ Updated issue' (false success); got:\n%s", out)
		}
		if !strings.Contains(strings.ToLower(out), "no change") {
			t.Errorf("expected an honest 'no change' line on a scalar-only no-op update; got:\n%s", out)
		}
	})

	t.Run("scalar_same_priority_reports_no_change", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "Noop priority", "--type", "task", "--priority", "2")
		out := bdCommand(t, bd, dir, "update", issue.ID, "--priority", "2")
		if strings.Contains(out, "Updated issue") {
			t.Errorf("re-setting --priority to the current value must NOT print '✓ Updated issue'; got:\n%s", out)
		}
		if !strings.Contains(strings.ToLower(out), "no change") {
			t.Errorf("expected 'no change' on a same-priority update; got:\n%s", out)
		}
	})

	t.Run("scalar_same_but_append_notes_still_updates", func(t *testing.T) {
		// The GUARD: --append-notes is always a real mutation (non-idempotent),
		// so even with a same-value scalar it must report "✓ Updated", never
		// "no change" — the display fix must not swallow the note append.
		issue := bdCreate(t, bd, dir, "Noop with notes", "--type", "task")
		out := bdCommand(t, bd, dir, "update", issue.ID, "--status", "open", "--append-notes", "a real note")
		if !strings.Contains(out, "Updated issue") {
			t.Errorf("a same-value scalar combined with --append-notes must still print '✓ Updated issue' (the note is a real change); got:\n%s", out)
		}
		if strings.Contains(strings.ToLower(out), "no change") {
			t.Errorf("must NOT report 'no change' when --append-notes is set — that would hide a real mutation; got:\n%s", out)
		}
	})

	t.Run("scalar_different_value_still_updates", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "Real change", "--type", "task", "--priority", "2")
		out := bdCommand(t, bd, dir, "update", issue.ID, "--priority", "0")
		if !strings.Contains(out, "Updated issue") {
			t.Errorf("a real scalar change (priority 2->0) must still print '✓ Updated issue'; got:\n%s", out)
		}
		if strings.Contains(strings.ToLower(out), "no change") {
			t.Errorf("a real change must NOT report 'no change'; got:\n%s", out)
		}
	})
}
