//go:build cgo

package main

import (
	"os"
	"strings"
	"testing"
)

// TestEmbeddedReopenDuplicateGuard is the teeth for beads-8nugc — the structural
// twin of the beads-8sjb3 supersede reopen guard. `bd duplicate old --of canonical`
// adds a `duplicates` edge (old→canonical) and closes old. Reopening old used to
// leave that edge, producing the contradictory "open but duplicate of canonical"
// state — and since `duplicates` is non-blocking, old reappeared in `bd ready`
// as actionable work. The reopen guard (mirroring the 8sjb3 supersede guard) now
// refuses unless --force.
func TestEmbeddedReopenDuplicateGuard(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "rdg")

	t.Run("reopen_duplicate_refused", func(t *testing.T) {
		canonical := bdCreate(t, bd, dir, "Canonical issue", "--type", "task")
		dup := bdCreate(t, bd, dir, "The duplicate", "--type", "task")
		// `bd duplicate <dup> --of <canonical>` closes dup + adds duplicates edge.
		bdDuplicate(t, bd, dir, dup.ID, "--of", canonical.ID)

		out := bdReopenFail(t, bd, dir, dup.ID)
		if !strings.Contains(out, "duplicate of") {
			t.Errorf("expected a 'duplicate of' guard error reopening %s, got: %s", dup.ID, out)
		}
	})

	t.Run("force_overrides_guard", func(t *testing.T) {
		canonical := bdCreate(t, bd, dir, "Canonical 2", "--type", "task")
		dup := bdCreate(t, bd, dir, "The duplicate 2", "--type", "task")
		bdDuplicate(t, bd, dir, dup.ID, "--of", canonical.ID)

		// --force must bypass the guard and reopen the issue.
		out := bdReopen(t, bd, dir, dup.ID, "--force")
		if !strings.Contains(out, "Reopened") {
			t.Errorf("expected --force to reopen the duplicate issue, got: %s", out)
		}
	})

	t.Run("normal_reopen_unaffected", func(t *testing.T) {
		// A plain closed issue with no duplicates edge must still reopen cleanly.
		iss := bdCreate(t, bd, dir, "Plain closed", "--type", "task")
		bdClose(t, bd, dir, iss.ID)
		out := bdReopen(t, bd, dir, iss.ID)
		if !strings.Contains(out, "Reopened") {
			t.Errorf("plain reopen (no duplicates) should succeed, got: %s", out)
		}
	})
}
