//go:build cgo

package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestEmbeddedReopenGuardWidenH7uhe is the beads-h7uhe teeth. The child-reopen
// close-guard (beads-b0tw) refuses reopening a closed child whose auto-closing
// parent is itself closed — but it was gated NARROWLY on newStatus ==
// StatusOpen. So the OTHER out-of-closed transitions bypassed it and silently
// recreated the forbidden closed-parent-with-non-closed-child state:
//
//	(1) bd update --status deferred   (closed -> deferred)
//	(2) bd update --status in_progress (closed -> in_progress)
//	(3) bd defer                       (closed -> deferred, its own last unguarded reopen side-door)
//
// The fix widens the predicate at all three guard sites (update.go,
// update_proxied_server.go, defer.go) to "was closed AND target is NOT closed"
// so every out-of-closed leg enforces it identically, with --force override.
//
// The co-located supersede/duplicate guards (beads-50dto) STAY scoped to
// closed->open — their harm (reappearing in `bd ready`) is open-only — so a
// closed->deferred transition must NOT trip a supersede/duplicate guard.
//
// Mutation: revert any of the three predicates back to `== StatusOpen` → that
// path's REFUSE subtest goes RED (the transition succeeds, rc=0).
func TestEmbeddedReopenGuardWidenH7uhe(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "rgw")

	// seedClosedParentChild creates a molecule root + one parent-child step, then
	// closes the step. The last step closing auto-closes the molecule root, so we
	// end with a CLOSED root and a CLOSED child. Returns the closed step id.
	seedClosedParentChild := func(t *testing.T, prefix string) (rootID, stepID string) {
		t.Helper()
		root := bdCreate(t, bd, dir, prefix+" root", "--type", "molecule")
		step := bdCreate(t, bd, dir, prefix+" step", "--type", "task")
		bdDepAdd(t, bd, dir, step.ID, root.ID, "--type", "parent-child")
		bdClose(t, bd, dir, step.ID) // last step complete → root auto-closes
		if got := bdShow(t, bd, dir, root.ID); got.Status != types.StatusClosed {
			t.Fatalf("precondition: root %s should have auto-closed, got %q", root.ID, got.Status)
		}
		if got := bdShow(t, bd, dir, step.ID); got.Status != types.StatusClosed {
			t.Fatalf("precondition: step %s should be closed, got %q", step.ID, got.Status)
		}
		return root.ID, step.ID
	}

	// deferFail runs `bd defer` expecting a non-zero exit (all ids refused).
	deferFail := func(t *testing.T, args ...string) string {
		t.Helper()
		cmd := exec.Command(bd, append([]string{"defer"}, args...)...)
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Fatalf("expected bd defer %s to fail, but it succeeded:\n%s", strings.Join(args, " "), out)
		}
		return string(out)
	}

	// (1) update --status deferred: the widened-predicate leg. Was rc=0 pre-fix.
	t.Run("update_status_deferred_refuses_without_force", func(t *testing.T) {
		_, step := seedClosedParentChild(t, "upd-def")
		out := bdUpdateFail(t, bd, dir, step, "--status", "deferred")
		if !strings.Contains(out, "its parent") || !strings.Contains(out, "is closed") {
			t.Errorf("expected closed-parent guard on update --status deferred, got:\n%s", out)
		}
		if got := bdShow(t, bd, dir, step); got.Status != types.StatusClosed {
			t.Errorf("step must remain closed after refused update, got %s", got.Status)
		}
	})

	t.Run("update_status_deferred_force_succeeds", func(t *testing.T) {
		_, step := seedClosedParentChild(t, "upd-deff")
		bdUpdate(t, bd, dir, step, "--status", "deferred", "--force")
		if got := bdShow(t, bd, dir, step); got.Status != types.StatusDeferred {
			t.Errorf("expected step deferred with --force, got %s", got.Status)
		}
	})

	// (2) update --status in_progress: same widened leg, different target status.
	t.Run("update_status_in_progress_refuses_without_force", func(t *testing.T) {
		_, step := seedClosedParentChild(t, "upd-ip")
		out := bdUpdateFail(t, bd, dir, step, "--status", "in_progress")
		if !strings.Contains(out, "its parent") || !strings.Contains(out, "is closed") {
			t.Errorf("expected closed-parent guard on update --status in_progress, got:\n%s", out)
		}
		if got := bdShow(t, bd, dir, step); got.Status != types.StatusClosed {
			t.Errorf("step must remain closed after refused update, got %s", got.Status)
		}
	})

	// (3) bd defer: the last unguarded reopen side-door (defer.go wrote via
	//     UpdateIssue with no closedEpicParents check).
	t.Run("defer_refuses_without_force", func(t *testing.T) {
		_, step := seedClosedParentChild(t, "def")
		out := deferFail(t, step)
		if !strings.Contains(out, "its parent") || !strings.Contains(out, "is closed") {
			t.Errorf("expected closed-parent guard on bd defer, got:\n%s", out)
		}
		if got := bdShow(t, bd, dir, step); got.Status != types.StatusClosed {
			t.Errorf("step must remain closed after refused defer, got %s", got.Status)
		}
	})

	t.Run("defer_force_succeeds", func(t *testing.T) {
		_, step := seedClosedParentChild(t, "deff")
		bdDefer(t, bd, dir, step, "--force")
		if got := bdShow(t, bd, dir, step); got.Status != types.StatusDeferred {
			t.Errorf("expected step deferred with --force, got %s", got.Status)
		}
	})

	// NEGATIVE (regression): the closed->open leg (bd update --status open) still
	// refuses exactly as before — the widen must not have weakened the original
	// b0tw guard.
	t.Run("update_status_open_still_refuses", func(t *testing.T) {
		_, step := seedClosedParentChild(t, "upd-open")
		out := bdUpdateFail(t, bd, dir, step, "--status", "open")
		if !strings.Contains(out, "its parent") || !strings.Contains(out, "is closed") {
			t.Errorf("expected closed-parent guard still fires on update --status open, got:\n%s", out)
		}
	})

	// NEGATIVE (regression): an OPEN child under an OPEN (plain, non-auto-closing)
	// epic parent may still be deferred freely — the guard only fires on a CLOSED
	// parent, so legitimate defers must not be blocked.
	t.Run("open_child_open_epic_defer_unaffected", func(t *testing.T) {
		epic := bdCreate(t, bd, dir, "plain epic", "--type", "epic")
		child := bdCreate(t, bd, dir, "epic child", "--type", "task")
		bdDepAdd(t, bd, dir, child.ID, epic.ID, "--type", "parent-child")
		if got := bdShow(t, bd, dir, epic.ID); got.Status == types.StatusClosed {
			t.Fatalf("precondition: plain epic must stay open, got %s", got.Status)
		}
		bdDefer(t, bd, dir, child.ID)
		if got := bdShow(t, bd, dir, child.ID); got.Status != types.StatusDeferred {
			t.Errorf("defer of open child under open epic should succeed, got %s", got.Status)
		}
	})

	// NEGATIVE (scope): the co-located supersede guard (beads-50dto) must STAY
	// scoped to closed->open — its harm (a superseded issue reappearing in `bd
	// ready`) is status=open-only. A superseded closed issue (NO closed parent):
	//   - update --status open   → REFUSED (supersede guard, closed->open)
	//   - update --status deferred → SUCCEEDS (deferred is not ready-visible, and
	//     the widened closed-parent guard has no closed parent to fire on).
	// This proves the h7uhe widen did NOT accidentally extend the supersede guard
	// to non-open transitions.
	t.Run("superseded_closed_open_refuses_but_deferred_succeeds", func(t *testing.T) {
		oldA := bdCreate(t, bd, dir, "sup old A", "--type", "task")
		newA := bdCreate(t, bd, dir, "sup new A", "--type", "task")
		bdSupersede(t, bd, dir, oldA.ID, "--with", newA.ID) // closes oldA + supersedes edge
		if got := bdShow(t, bd, dir, oldA.ID); got.Status != types.StatusClosed {
			t.Fatalf("precondition: superseded issue should be closed, got %s", got.Status)
		}
		out := bdUpdateFail(t, bd, dir, oldA.ID, "--status", "open")
		if !strings.Contains(out, "superseded by") {
			t.Errorf("expected supersede guard on closed->open, got:\n%s", out)
		}

		oldB := bdCreate(t, bd, dir, "sup old B", "--type", "task")
		newB := bdCreate(t, bd, dir, "sup new B", "--type", "task")
		bdSupersede(t, bd, dir, oldB.ID, "--with", newB.ID)
		// closed->deferred: supersede guard is out of scope, no closed parent →
		// must succeed.
		bdUpdate(t, bd, dir, oldB.ID, "--status", "deferred")
		if got := bdShow(t, bd, dir, oldB.ID); got.Status != types.StatusDeferred {
			t.Errorf("closed->deferred on a superseded (no closed parent) issue should succeed, got %s", got.Status)
		}
	})
}
