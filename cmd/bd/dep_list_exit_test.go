//go:build cgo

package main

import (
	"os"
	"strings"
	"testing"
)

// TestEmbeddedDepListExitCode covers beads-116e: `bd dep list <id>...` in batch
// mode (len(args)>1) warn+continued on each failed resolve and returned nil at
// every terminal, so rc=0 even when a valid+ghost mix partially failed OR every
// id was a ghost ("no resolvable issues in batch" still returned nil). This
// diverged from the single-id path (rc=1, errors at resolve). Because a
// `bd dep list $ids || alert` guard is a common script pattern, the silent
// success meant a typo'd/missing id proceeded as if listed. The command must
// exit non-zero when any id fails to resolve in batch, while still listing the
// resolvable ids (partial output preserved). Same silent-partial-failure
// exit-code class as beads-sw7l / beads-2svv (bd show) and beads-xi35
// (bd todo done).
func TestEmbeddedDepListExitCode(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "dl")

	a := bdCreate(t, bd, dir, "dl real A", "--type", "task")
	b := bdCreate(t, bd, dir, "dl real B", "--type", "task")

	t.Run("single_valid_exits_zero", func(t *testing.T) {
		// baseline: a single resolvable id succeeds.
		bdDep(t, bd, dir, "list", a.ID)
	})

	t.Run("single_ghost_exits_nonzero", func(t *testing.T) {
		// baseline for the correct contract: single-id resolve failure = rc!=0.
		bdDepFail(t, bd, dir, "list", "dl-ghost-x")
	})

	t.Run("batch_all_ghost_exits_nonzero", func(t *testing.T) {
		bdDepFail(t, bd, dir, "list", "dl-ghost-a", "dl-ghost-b")
	})

	t.Run("batch_partial_exits_nonzero_still_lists_valid", func(t *testing.T) {
		// valid id + a ghost: must exit non-zero, but the valid id must still be
		// resolved/listed (partial output preserved). The valid id's line is on
		// stdout; the ghost warning is on stderr — CombinedOutput sees both.
		out := bdDepFail(t, bd, dir, "list", a.ID, "dl-ghost-z")
		if !strings.Contains(out, a.ID) {
			t.Errorf("expected valid issue %s still listed on partial failure, got:\n%s", a.ID, out)
		}
		if !strings.Contains(out, "dl-ghost-z") {
			t.Errorf("expected the skipped ghost id reported, got:\n%s", out)
		}
	})

	t.Run("batch_all_valid_exits_zero", func(t *testing.T) {
		// two resolvable ids, no failures: rc=0 (regression guard — the fix must
		// not turn a clean batch into a failure).
		bdDep(t, bd, dir, "list", a.ID, b.ID)
	})
}
