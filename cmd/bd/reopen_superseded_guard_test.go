//go:build cgo

package main

import (
	"os"
	"strings"
	"testing"
)

// TestEmbeddedReopenSupersededGuard is the teeth for beads-8sjb3 (DISCOVERY.md
// BUG-22). `bd supersede old --with new` adds a `supersedes` edge (old→new) and
// closes old. Reopening old used to leave that edge, producing the contradictory
// "open but superseded by new" state — and since `supersedes` is non-blocking,
// old reappeared in `bd ready`. The reopen guard (mirroring the b0tw
// closed-epic-parent guard) now refuses unless --force.
func TestEmbeddedReopenSupersededGuard(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "rsg")

	t.Run("reopen_superseded_refused", func(t *testing.T) {
		oldIssue := bdCreate(t, bd, dir, "Superseded old", "--type", "task")
		newIssue := bdCreate(t, bd, dir, "Replacement new", "--type", "task")
		bdSupersede(t, bd, dir, oldIssue.ID, "--with", newIssue.ID)

		out := bdReopenFail(t, bd, dir, oldIssue.ID)
		if !strings.Contains(out, "superseded by") {
			t.Errorf("expected a 'superseded by' guard error reopening %s, got: %s", oldIssue.ID, out)
		}
	})

	t.Run("force_overrides_guard", func(t *testing.T) {
		oldIssue := bdCreate(t, bd, dir, "Superseded old 2", "--type", "task")
		newIssue := bdCreate(t, bd, dir, "Replacement new 2", "--type", "task")
		bdSupersede(t, bd, dir, oldIssue.ID, "--with", newIssue.ID)

		// --force must bypass the guard and reopen the issue.
		out := bdReopen(t, bd, dir, oldIssue.ID, "--force")
		if !strings.Contains(out, "Reopened") {
			t.Errorf("expected --force to reopen the superseded issue, got: %s", out)
		}
	})

	t.Run("normal_reopen_unaffected", func(t *testing.T) {
		// A plain closed issue with no supersedes edge must still reopen cleanly.
		iss := bdCreate(t, bd, dir, "Plain closed", "--type", "task")
		bdClose(t, bd, dir, iss.ID)
		out := bdReopen(t, bd, dir, iss.ID)
		if !strings.Contains(out, "Reopened") {
			t.Errorf("plain reopen (no supersedes) should succeed, got: %s", out)
		}
	})
}
