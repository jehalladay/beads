//go:build cgo

package main

import (
	"strings"
	"testing"
)

// TestProxiedDepTreeGroundTruthVerdict proves the proxied (hub-connected)
// dep-tree path derives the root's READY/BLOCKED verdict from ground truth
// (DependencyUseCase.IsBlocked) rather than from the possibly-truncated or
// reversed children slice — mirroring the direct-path fixes beads-x2e9
// (--max-depth cap truncates blockers → false [READY]) and beads-wucv
// (--reverse renders dependents, not blockers → false [BLOCKED]).
//
// Before beads-8xwpb the proxied handler (dep_proxied_server.go) called
// renderTree(...) which forwarded a nil rootBlockedOverride, so both bugs were
// still live on this twin even though the direct path was fixed. The proxied
// verdict test coverage did not exist (x2e9/wucv teeth were direct-path only).
func TestProxiedDepTreeGroundTruthVerdict(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)
	p := bdProxiedInit(t, bd, "pxv")

	// Chain: c depends-on b depends-on a  (a blocks b blocks c).
	a := bdProxiedCreate(t, bd, p.dir, "root blocker A", "--type", "task")
	b := bdProxiedCreate(t, bd, p.dir, "mid B", "--type", "task")
	c := bdProxiedCreate(t, bd, p.dir, "leaf C", "--type", "task")
	bdProxiedDep(t, bd, p.dir, "add", b.ID, a.ID) // b depends on a
	bdProxiedDep(t, bd, p.dir, "add", c.ID, b.ID) // c depends on b

	// Ground truth: c is blocked (open dependency b).
	t.Run("full_depth_blocked", func(t *testing.T) {
		out := bdProxiedDep(t, bd, p.dir, "tree", c.ID)
		if !strings.Contains(out, "[BLOCKED]") {
			t.Errorf("full-depth proxied tree should show [BLOCKED] for c:\n%s", out)
		}
	})

	// beads-x2e9 (proxied twin): --max-depth=1 truncates the depth-1 blocker
	// from children[root]; the ground-truth override must still show [BLOCKED].
	t.Run("max_depth_1_still_blocked", func(t *testing.T) {
		out := bdProxiedDep(t, bd, p.dir, "tree", c.ID, "--max-depth=1")
		if strings.Contains(out, "[READY]") {
			t.Errorf("proxied --max-depth=1 wrongly shows [READY] for a genuinely-blocked root (beads-8xwpb/x2e9):\n%s", out)
		}
		if !strings.Contains(out, "[BLOCKED]") {
			t.Errorf("proxied --max-depth=1 should still show [BLOCKED] via ground-truth verdict:\n%s", out)
		}
	})

	t.Run("unblocked_root_ready", func(t *testing.T) {
		// a has no open dependencies → READY at any depth.
		out := bdProxiedDep(t, bd, p.dir, "tree", a.ID, "--max-depth=1")
		if !strings.Contains(out, "[READY]") {
			t.Errorf("proxied unblocked root a should show [READY]:\n%s", out)
		}
	})

	// beads-wucv (proxied twin): --reverse (up) view of an unblocked root that
	// HAS dependents must show [READY]. a blocks b (b depends on a), so a has a
	// dependent but zero dependencies → genuinely READY.
	t.Run("reverse_unblocked_root_with_dependents_ready", func(t *testing.T) {
		out := bdProxiedDep(t, bd, p.dir, "tree", a.ID, "--reverse")
		if strings.Contains(out, "[BLOCKED]") {
			t.Errorf("proxied --reverse wrongly shows [BLOCKED] for an unblocked root with dependents (beads-8xwpb/wucv):\n%s", out)
		}
		if !strings.Contains(out, "[READY]") {
			t.Errorf("proxied --reverse should show [READY] for the unblocked root a:\n%s", out)
		}
	})
}
