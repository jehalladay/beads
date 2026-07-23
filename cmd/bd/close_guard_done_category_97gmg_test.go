//go:build cgo

package main

import (
	"os"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestEmbeddedCloseGuardDoneCategory_97gmg is the beads-97gmg teeth: the
// auto-closing-parent close guard counted a child in a custom DONE-category
// status (e.g. `bd config set status.custom "resolved:done"`) as an OPEN child,
// so an epic/molecule/wisp root whose only remaining children were done-category
// could neither auto-close NOR be manually closed — `bd close` returned
//
//	cannot close: N open child issue(s); close children first or use --force
//
// diverging from molecule completion (beads-x463g), the dep-completeness legs,
// and bd ready/count/list, all of which treat a done-category status as terminal.
//
// The fix threads the configured done-category status names into the child-count
// helpers so a done-category child counts as complete (childCountsAsOpen):
//   - direct:  countEpicOpenChildren (close.go) + countEpicOpenChildrenExcluding
//     (batch.go) + openChildIDsOfEpic (lint.go)
//   - proxied: countOpenChildren (domain/issue.go) + openChildIDsOfEpic
//     (lint_proxied_server.go)
//
// MUTATION-VERIFY: revert childCountsAsOpen to the bare `status !=
// types.StatusClosed` test → the done-category close/demote legs go RED (the
// close is refused) while the negatives stay green.
func TestEmbeddedCloseGuardDoneCategory_97gmg(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)

	// seedRootWithDoneChild creates a root (epic or molecule) with one child,
	// registers a custom done-category status "resolved", and moves the child
	// into it. Returns the still-open root and the done-category child.
	seedRootWithDoneChild := func(t *testing.T, dir string, rootArgs ...string) (root, child *types.Issue) {
		t.Helper()
		bdConfig(t, bd, dir, "set", "status.custom", "resolved:done")
		root = bdCreate(t, bd, dir, append([]string{"root"}, rootArgs...)...)
		child = bdCreate(t, bd, dir, "child", "--type", "task")
		bdDepAdd(t, bd, dir, child.ID, root.ID, "--type", "parent-child")
		bdUpdate(t, bd, dir, child.ID, "--status", "resolved")
		if got := bdShow(t, bd, dir, child.ID); got.Status != types.Status("resolved") {
			t.Fatalf("precondition: child should be in custom done status 'resolved', got %q", got.Status)
		}
		return root, child
	}

	// (1) `bd close` on an epic whose only child is done-category must SUCCEED
	//     without --force — the child counts as complete.
	t.Run("close_epic_with_done_category_child_succeeds", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "gc1")
		root, _ := seedRootWithDoneChild(t, dir, "--type", "epic")
		bdClose(t, bd, dir, root.ID) // must not be refused
		if got := bdShow(t, bd, dir, root.ID); got.Status != types.StatusClosed {
			t.Errorf("epic with only a done-category child should close, got %q", got.Status)
		}
	})

	// (1b) same for a molecule root (the guard fires for any auto-closing parent).
	t.Run("close_molecule_with_done_category_child_succeeds", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "gc2")
		root, _ := seedRootWithDoneChild(t, dir, "--type", "molecule")
		bdClose(t, bd, dir, root.ID)
		if got := bdShow(t, bd, dir, root.ID); got.Status != types.StatusClosed {
			t.Errorf("molecule with only a done-category child should close, got %q", got.Status)
		}
	})

	// (2) the `bd update --status closed` close-guard twin (update.go:615) must
	//     agree with `bd close`.
	t.Run("update_status_closed_epic_with_done_category_child_succeeds", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "gc3")
		root, _ := seedRootWithDoneChild(t, dir, "--type", "epic")
		if out, err := bdRunWithFlockRetry(t, bd, dir, "update", root.ID, "--status", "closed"); err != nil {
			t.Fatalf("update --status closed on epic with done-category child should succeed: %v\n%s", err, out)
		}
		if got := bdShow(t, bd, dir, root.ID); got.Status != types.StatusClosed {
			t.Errorf("epic should be closed via update, got %q", got.Status)
		}
	})

	// (3) LINT lockstep: after the epic legitimately closes with an all-done
	//     child, `bd lint` must NOT flag it as a closed-epic-with-open-child
	//     inconsistency (openChildIDsOfEpic is done-aware in lockstep).
	t.Run("lint_does_not_flag_closed_epic_with_done_child", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "gc4")
		root, _ := seedRootWithDoneChild(t, dir, "--type", "epic")
		bdClose(t, bd, dir, root.ID)
		out, _ := bdLint(t, bd, dir)
		if strings.Contains(out, root.ID) && strings.Contains(strings.ToLower(out), "open child") {
			t.Errorf("lint must not flag a closed epic whose only child is done-category as having open children:\n%s", out)
		}
	})

	// NEGATIVE (a): a genuinely OPEN (non-done) child still blocks the close —
	//     the guard is not disabled wholesale.
	t.Run("open_child_still_blocks_close", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "gc5")
		bdConfig(t, bd, dir, "set", "status.custom", "resolved:done")
		root := bdCreate(t, bd, dir, "root", "--type", "epic")
		child := bdCreate(t, bd, dir, "child", "--type", "task")
		bdDepAdd(t, bd, dir, child.ID, root.ID, "--type", "parent-child")
		out := bdCloseFail(t, bd, dir, root.ID)
		if !strings.Contains(strings.ToLower(out), "open child") {
			t.Errorf("an open (non-done) child must still block the epic close, got:\n%s", out)
		}
		if got := bdShow(t, bd, dir, root.ID); got.Status == types.StatusClosed {
			t.Errorf("epic must remain open while it has a genuinely open child")
		}
	})

	// NEGATIVE (b): a FROZEN-category custom status is NOT a completion (the
	//     views exclude it from ready but molecule completion counts only Done),
	//     so a frozen-category child still blocks the close — mirrors the
	//     Done-only semantics of the fix.
	t.Run("frozen_category_child_still_blocks_close", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "gc6")
		bdConfig(t, bd, dir, "set", "status.custom", "parked:frozen")
		root := bdCreate(t, bd, dir, "root", "--type", "epic")
		child := bdCreate(t, bd, dir, "child", "--type", "task")
		bdDepAdd(t, bd, dir, child.ID, root.ID, "--type", "parent-child")
		bdUpdate(t, bd, dir, child.ID, "--status", "parked")
		out := bdCloseFail(t, bd, dir, root.ID)
		if !strings.Contains(strings.ToLower(out), "open child") {
			t.Errorf("a frozen-category (not done) child must still block the epic close, got:\n%s", out)
		}
	})
}
