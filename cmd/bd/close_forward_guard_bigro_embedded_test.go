//go:build cgo

package main

import (
	"os"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestEmbeddedForwardCloseGuardAutoClosingParent is the beads-bigro teeth: the
// FORWARD close-time guard ("cannot close <parent> with open children") only
// tested bare TypeEpic, while beads-aw9x8 widened only the BACKWARD
// (reopen/dep-add) guards to the shared isAutoClosingParentType predicate (epic
// OR molecule OR ephemeral). So a MOLECULE or wisp/ephemeral root could be
// MANUALLY closed while it still had open children — reaching the exact
// closed-parent-with-open-child state the close-guard family (2hkd/b0tw/eth8)
// exists to prevent, via the forward path aw9x8 left uncovered.
//
// The fix routes the forward close guard (close.go / close_proxied_server.go)
// through the same isAutoClosingParentType helper. These are the subprocess
// (real `bd`) teeth — mutation: restore the bare `IssueType == types.TypeEpic`
// test in close.go's forward guard → the molecule + wisp cases below go from
// rc!=0 back to rc=0 (RED). The plain-task and open-epic controls stay GREEN,
// proving the widening is precise (does not block a non-parent close).
func TestEmbeddedForwardCloseGuardAutoClosingParent(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "fcg")

	// seedRootWithOpenChild builds a root of the given create args with a single
	// OPEN parent-child step (never closed) — so the root still has an open
	// child at the moment we attempt to close the root.
	seedRootWithOpenChild := func(t *testing.T, prefix string, rootArgs ...string) (root, child *types.Issue) {
		t.Helper()
		root = bdCreate(t, bd, dir, append([]string{prefix + " root"}, rootArgs...)...)
		child = bdCreate(t, bd, dir, prefix+" child", "--type", "task")
		bdDepAdd(t, bd, dir, child.ID, root.ID, "--type", "parent-child")
		if got := bdShow(t, bd, dir, child.ID); got.Status != types.StatusOpen {
			t.Fatalf("precondition: child %s must be open, got %q", child.ID, got.Status)
		}
		return root, child
	}

	// (1) MOLECULE root with an open child: `bd close <root>` must refuse
	//     (the forward-direction guard aw9x8 left uncovered), overridable
	//     with --force.
	t.Run("molecule_close_with_open_child_refuses", func(t *testing.T) {
		root, child := seedRootWithOpenChild(t, "mol", "--type", "molecule")
		out := bdCloseFail(t, bd, dir, root.ID)
		if !strings.Contains(out, "open child") {
			t.Errorf("expected open-child close guard for molecule root, got:\n%s", out)
		}
		if got := bdShow(t, bd, dir, root.ID); got.Status != types.StatusOpen {
			t.Errorf("molecule root must remain open after a refused close, got %s", got.Status)
		}
		if got := bdShow(t, bd, dir, child.ID); got.Status != types.StatusOpen {
			t.Errorf("child must remain open, got %s", got.Status)
		}
	})

	t.Run("molecule_close_with_open_child_force_succeeds", func(t *testing.T) {
		root, _ := seedRootWithOpenChild(t, "molf", "--type", "molecule")
		bdClose(t, bd, dir, root.ID, "--force")
		if got := bdShow(t, bd, dir, root.ID); got.Status != types.StatusClosed {
			t.Errorf("--force should close a molecule root with open children, got %s", got.Status)
		}
	})

	// (2) EPHEMERAL (wisp) root with an open child: the same forward guard must
	//     fire. isAutoClosingParentType's Ephemeral branch is what this
	//     exercises. Mutation: drop `|| issue.Ephemeral` from the helper (or
	//     restore the epic-only forward test) → this goes RED (root closes rc=0).
	t.Run("wisp_close_with_open_child_refuses", func(t *testing.T) {
		root, _ := seedRootWithOpenChild(t, "wisp", "--type", "task", "--ephemeral")
		out := bdCloseFail(t, bd, dir, root.ID)
		if !strings.Contains(out, "open child") {
			t.Errorf("expected open-child close guard for ephemeral root, got:\n%s", out)
		}
		if got := bdShow(t, bd, dir, root.ID); got.Status != types.StatusOpen {
			t.Errorf("ephemeral root must remain open after a refused close, got %s", got.Status)
		}
	})

	// (3) EPIC control (regression): the guard already fired for epics; it must
	//     still fire (aw9x8/b0tw invariant preserved on the forward path).
	t.Run("epic_close_with_open_child_still_refuses", func(t *testing.T) {
		root, _ := seedRootWithOpenChild(t, "epic", "--type", "epic")
		out := bdCloseFail(t, bd, dir, root.ID)
		if !strings.Contains(out, "open child") {
			t.Errorf("expected open-child close guard for epic root (unchanged), got:\n%s", out)
		}
		if got := bdShow(t, bd, dir, root.ID); got.Status != types.StatusOpen {
			t.Errorf("epic root must remain open after a refused close, got %s", got.Status)
		}
	})

	// (4) PRECISION control: a plain (non-auto-closing) parent — a `task` with a
	//     parent-child open child — must still close FREELY (rc=0). The widening
	//     must not start blocking a plain-type parent close. Mutation-inverse: if
	//     the guard were widened to ALL types this would go RED (close refused).
	t.Run("plain_task_parent_close_with_open_child_succeeds", func(t *testing.T) {
		root, child := seedRootWithOpenChild(t, "task", "--type", "task")
		bdClose(t, bd, dir, root.ID)
		if got := bdShow(t, bd, dir, root.ID); got.Status != types.StatusClosed {
			t.Errorf("a plain task parent with an open child must close freely, got %s", got.Status)
		}
		// The child is untouched (still open).
		if got := bdShow(t, bd, dir, child.ID); got.Status != types.StatusOpen {
			t.Errorf("child must remain open, got %s", got.Status)
		}
	})
}
