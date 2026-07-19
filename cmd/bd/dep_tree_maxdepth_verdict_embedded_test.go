package main

import (
	"os"
	"strings"
	"testing"
)

// TestEmbeddedDepTreeMaxDepthVerdict proves a display --max-depth cap cannot
// flip a genuinely-BLOCKED root to [READY] (beads-x2e9). Before the fix the
// root verdict was derived from the depth-truncated rendered children, so
// --max-depth=1 (which truncates the depth-1 blockers) wrongly showed a blocked
// root as [READY]. The fix computes the verdict from ground truth
// (store.IsBlocked) for the down/default direction.
func TestEmbeddedDepTreeMaxDepthVerdict(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "dmv")

	// Chain: c depends-on b depends-on a  (a blocks b blocks c).
	a := bdCreate(t, bd, dir, "root blocker A", "--type", "task")
	b := bdCreate(t, bd, dir, "mid B", "--type", "task")
	c := bdCreate(t, bd, dir, "leaf C", "--type", "task")
	bdDep(t, bd, dir, "add", b.ID, a.ID) // b depends on a
	bdDep(t, bd, dir, "add", c.ID, b.ID) // c depends on b

	// Ground truth: c is blocked (open dependency b).
	t.Run("full_depth_blocked", func(t *testing.T) {
		out := bdDep(t, bd, dir, "tree", c.ID)
		if !strings.Contains(out, "[BLOCKED]") {
			t.Errorf("full-depth tree should show [BLOCKED] for c:\n%s", out)
		}
	})

	t.Run("max_depth_1_still_blocked", func(t *testing.T) {
		out := bdDep(t, bd, dir, "tree", c.ID, "--max-depth=1")
		if strings.Contains(out, "[READY]") {
			t.Errorf("--max-depth=1 wrongly shows [READY] for a genuinely-blocked root (beads-x2e9):\n%s", out)
		}
		if !strings.Contains(out, "[BLOCKED]") {
			t.Errorf("--max-depth=1 should still show [BLOCKED] via ground-truth verdict:\n%s", out)
		}
	})

	t.Run("max_depth_2_blocked", func(t *testing.T) {
		out := bdDep(t, bd, dir, "tree", c.ID, "--max-depth=2")
		if !strings.Contains(out, "[BLOCKED]") {
			t.Errorf("--max-depth=2 should show [BLOCKED]:\n%s", out)
		}
	})

	t.Run("unblocked_root_ready", func(t *testing.T) {
		// a has no open dependencies → READY at any depth.
		out := bdDep(t, bd, dir, "tree", a.ID, "--max-depth=1")
		if !strings.Contains(out, "[READY]") {
			t.Errorf("unblocked root a should show [READY]:\n%s", out)
		}
	})
}
