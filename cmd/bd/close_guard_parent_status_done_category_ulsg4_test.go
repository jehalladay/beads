//go:build cgo

package main

import (
	"os"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestEmbeddedCloseGuardParentStatusDoneCategory_ulsg4 is the beads-ulsg4 teeth:
// the PARENT-STATUS completion of the done-category close-guard family. beads-97gmg
// made the CHILD count done-aware (childCountsAsOpen), but the PARENT-status
// triggers across the guard family kept a literal `== types.StatusClosed` /
// `!= types.StatusClosed` test, so a parent moved to a CUSTOM done-category status
// (e.g. `bd config set status.custom "resolved:done"`) was treated as
// neither-closed-nor-open and BYPASSED every parent-side guard:
//
//   - forward update (update.go): the close-integrity guards fired only on
//     newStatus=="closed", so `bd update --status resolved` on an auto-closing
//     parent with open children silently created the forbidden
//     terminal-parent-with-open-child state — the WORST leg (no --force prompt,
//     and lint couldn't detect it post-hoc because its scan skipped it).
//   - reopen (close.go closedEpicParents, via reopen.go / update.go --status open /
//     batch.go): a child reopen under a done-category parent sailed past the guard.
//   - reparent (update.go): reparenting an open child under a done-category parent
//     recreated the state a literal-closed parent is guarded against.
//   - lint (lint.go scanClosedEpicsWithOpenChildren + closedEpicsToScan): STRICTLY
//     WORSE than a miss — the `!= StatusClosed` scan SKIPPED done-category parents,
//     so the bypass was undetectable after the fact.
//
// The fix threads doneCategoryStatusNames into a shared parentStatusIsTerminal
// helper (a literal-closed OR a done-category status is terminal), mirroring
// 97gmg's child-side widen. Frozen-category is EXCLUDED (parked != done).
//
// MUTATION-VERIFY: revert parentStatusIsTerminal to a bare
// `status == types.StatusClosed` → the done-category legs below go RED (the
// forbidden state is created / the reopen+reparent are allowed / lint stops
// flagging) while the literal-closed controls and the frozen negative stay green.
func TestEmbeddedCloseGuardParentStatusDoneCategory_ulsg4(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)

	// (A) FORWARD UPDATE — the worst leg: `bd update --status <done-category>` on
	//     an auto-closing parent with a genuinely OPEN child must be REFUSED
	//     without --force, exactly as `--status closed` is, because the
	//     done-category status is terminal.
	t.Run("forward_update_to_done_category_with_open_child_refused", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "upa")
		bdConfig(t, bd, dir, "set", "status.custom", "resolved:done")
		epic := bdCreate(t, bd, dir, "epic", "--type", "epic")
		child := bdCreate(t, bd, dir, "child", "--type", "task")
		bdDepAdd(t, bd, dir, child.ID, epic.ID, "--type", "parent-child")
		out := bdUpdateFail(t, bd, dir, epic.ID, "--status", "resolved")
		if !strings.Contains(strings.ToLower(out), "open child") {
			t.Errorf("moving an auto-closing parent to a done-category status with an open child must be refused, got:\n%s", out)
		}
		if got := bdShow(t, bd, dir, epic.ID); got.Status == types.Status("resolved") {
			t.Errorf("epic must not reach the done-category terminal status while it has an open child")
		}
	})

	// (A2) FORWARD UPDATE positive: with the only child in a done-category status,
	//      moving the parent to a done-category status must SUCCEED (the child
	//      counts as complete — 97gmg — and the parent target is a legit terminal).
	t.Run("forward_update_to_done_category_all_children_done_succeeds", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "upb")
		bdConfig(t, bd, dir, "set", "status.custom", "resolved:done")
		epic := bdCreate(t, bd, dir, "epic", "--type", "epic")
		child := bdCreate(t, bd, dir, "child", "--type", "task")
		bdDepAdd(t, bd, dir, child.ID, epic.ID, "--type", "parent-child")
		bdUpdate(t, bd, dir, child.ID, "--status", "resolved")
		if out, err := bdRunWithFlockRetry(t, bd, dir, "update", epic.ID, "--status", "resolved"); err != nil {
			t.Fatalf("update parent to done-category with all-done children should succeed: %v\n%s", err, out)
		}
		if got := bdShow(t, bd, dir, epic.ID); got.Status != types.Status("resolved") {
			t.Errorf("epic should reach the done-category status, got %q", got.Status)
		}
	})

	// (B) REOPEN — reopening a child whose parent is in a done-category status must
	//     be refused (closedEpicParents is now done-aware). Both axes: the `bd
	//     reopen` verb and `bd update --status open`.
	t.Run("reopen_child_under_done_category_parent_refused", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "rpa")
		root, child := seedDoneCategoryParentWithClosedChild(t, bd, dir)
		out := bdReopenFail(t, bd, dir, child.ID)
		if !strings.Contains(out, "its parent") || !strings.Contains(out, "is closed") {
			t.Errorf("reopen of a child under a done-category parent must be refused, got:\n%s", out)
		}
		if got := bdShow(t, bd, dir, child.ID); got.Status != types.StatusClosed {
			t.Errorf("child must remain closed after refused reopen, got %s", got.Status)
		}
		_ = root
	})

	t.Run("update_status_open_child_under_done_category_parent_refused", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "rpb")
		_, child := seedDoneCategoryParentWithClosedChild(t, bd, dir)
		out := bdUpdateFail(t, bd, dir, child.ID, "--status", "open")
		if !strings.Contains(out, "its parent") || !strings.Contains(out, "is closed") {
			t.Errorf("update --status open of a child under a done-category parent must be refused, got:\n%s", out)
		}
	})

	t.Run("reopen_child_under_done_category_parent_force_overrides", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "rpf")
		_, child := seedDoneCategoryParentWithClosedChild(t, bd, dir)
		bdReopen(t, bd, dir, child.ID, "--force")
		if got := bdShow(t, bd, dir, child.ID); got.Status != types.StatusOpen {
			t.Errorf("reopen with --force must override the done-category parent guard, got %s", got.Status)
		}
	})

	// (C) REPARENT — reparenting an OPEN child under a done-category parent must be
	//     refused (the parent-assignment axis), overridable with --force.
	t.Run("reparent_open_child_under_done_category_parent_refused", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "rea")
		bdConfig(t, bd, dir, "set", "status.custom", "resolved:done")
		epic := bdCreate(t, bd, dir, "epic", "--type", "epic")
		bdUpdate(t, bd, dir, epic.ID, "--status", "resolved") // no children yet
		child := bdCreate(t, bd, dir, "loose", "--type", "task")
		out := bdUpdateFail(t, bd, dir, child.ID, "--parent", epic.ID)
		if !strings.Contains(out, "closed parent") {
			t.Errorf("reparenting an open child under a done-category parent must be refused, got:\n%s", out)
		}
		for _, d := range showDeps(t, bd, dir, child.ID) {
			if d.ID == epic.ID {
				t.Errorf("open child must not be reparented under a done-category parent: %+v", d)
			}
		}
	})

	t.Run("reparent_open_child_under_done_category_parent_force_overrides", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "ref")
		bdConfig(t, bd, dir, "set", "status.custom", "resolved:done")
		epic := bdCreate(t, bd, dir, "epic", "--type", "epic")
		bdUpdate(t, bd, dir, epic.ID, "--status", "resolved")
		child := bdCreate(t, bd, dir, "loose", "--type", "task")
		bdUpdate(t, bd, dir, child.ID, "--parent", epic.ID, "--force")
	})

	// (D) LINT — a parent in a done-category status that STILL has a genuinely open
	//     child is a real structural inconsistency lint must SURFACE. Before ulsg4
	//     lint skipped done-category parents entirely (`!= StatusClosed`), so the
	//     forward-guard bypass was undetectable post-hoc; now they scan.
	t.Run("lint_flags_done_category_parent_with_open_child", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "lna")
		bdConfig(t, bd, dir, "set", "status.custom", "resolved:done")
		epic := bdCreate(t, bd, dir, "epic", "--type", "epic")
		child := bdCreate(t, bd, dir, "child", "--type", "task")
		bdDepAdd(t, bd, dir, child.ID, epic.ID, "--type", "parent-child")
		// Reach the forbidden state directly: --force past the forward guard so the
		// done-category parent ends up with an open child (the operator-override /
		// pre-guard-row case beads-4u7d lint exists to catch).
		bdUpdate(t, bd, dir, epic.ID, "--status", "resolved", "--force")
		out, _ := bdLint(t, bd, dir)
		if !strings.Contains(out, epic.ID) || !strings.Contains(strings.ToLower(out), "open child") {
			t.Errorf("lint must flag a done-category parent that still has an open child, got:\n%s", out)
		}
	})

	// (D2) LINT negative: a done-category parent whose only child is ALSO
	//      done-category is NOT an inconsistency (openChildIDsOfEpic is done-aware
	//      via 97gmg) — the scan widening must not manufacture false positives.
	t.Run("lint_does_not_flag_done_category_parent_with_done_child", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "lnb")
		bdConfig(t, bd, dir, "set", "status.custom", "resolved:done")
		epic := bdCreate(t, bd, dir, "epic", "--type", "epic")
		child := bdCreate(t, bd, dir, "child", "--type", "task")
		bdDepAdd(t, bd, dir, child.ID, epic.ID, "--type", "parent-child")
		bdUpdate(t, bd, dir, child.ID, "--status", "resolved")
		bdUpdate(t, bd, dir, epic.ID, "--status", "resolved")
		out, _ := bdLint(t, bd, dir)
		if strings.Contains(out, epic.ID) && strings.Contains(strings.ToLower(out), "open child") {
			t.Errorf("lint must not flag a done-category parent whose only child is also done-category:\n%s", out)
		}
	})

	// NEGATIVE (control): the LITERAL-closed parent paths are unchanged — the
	//     forward-close guard, reopen guard and reparent guard all still fire on a
	//     genuinely `closed` parent, proving the widen is additive.
	t.Run("literal_closed_parent_still_guards_reopen", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "lca")
		epic := bdCreate(t, bd, dir, "epic", "--type", "epic")
		child := bdCreate(t, bd, dir, "child", "--type", "task")
		bdDepAdd(t, bd, dir, child.ID, epic.ID, "--type", "parent-child")
		bdClose(t, bd, dir, child.ID)
		bdClose(t, bd, dir, epic.ID) // all children complete → closes
		out := bdReopenFail(t, bd, dir, child.ID)
		if !strings.Contains(out, "its parent") || !strings.Contains(out, "is closed") {
			t.Errorf("literal-closed parent must still guard child reopen (control), got:\n%s", out)
		}
	})

	// NEGATIVE: a FROZEN-category parent is NOT terminal (parked != done), so
	//     moving a parent to a frozen-category status is NOT a terminal transition —
	//     it does not enter the close-guard path at all (mirrors the Done-only
	//     semantics of 97gmg/x463g). The child reopen under a frozen-category parent
	//     is therefore NOT blocked by this guard.
	t.Run("frozen_category_parent_is_not_terminal", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "frz")
		bdConfig(t, bd, dir, "set", "status.custom", "parked:frozen")
		epic := bdCreate(t, bd, dir, "epic", "--type", "epic")
		child := bdCreate(t, bd, dir, "child", "--type", "task")
		bdDepAdd(t, bd, dir, child.ID, epic.ID, "--type", "parent-child")
		bdClose(t, bd, dir, child.ID)
		// Move the parent to a frozen-category status — parked, not done, so it is
		// not a terminal transition and does not trip the open-child close guard.
		if out, err := bdRunWithFlockRetry(t, bd, dir, "update", epic.ID, "--status", "parked"); err != nil {
			t.Fatalf("moving a parent to a frozen-category status must not be treated as a terminal close: %v\n%s", err, out)
		}
		// Reopening the (closed) child under a frozen-category parent must be allowed
		// — a frozen parent is not terminal, so the reopen guard does not fire.
		bdReopen(t, bd, dir, child.ID)
		if got := bdShow(t, bd, dir, child.ID); got.Status != types.StatusOpen {
			t.Errorf("reopen of a child under a frozen-category (not done) parent must succeed, got %s", got.Status)
		}
	})
}

// seedDoneCategoryParentWithClosedChild creates an epic with one child, registers
// a custom done-category status "resolved", closes the child, then moves the
// parent into the done-category status (via --force, so the move is not blocked
// while we set up the reopen/reparent scenarios). Returns the done-category parent
// and its now-closed child.
func seedDoneCategoryParentWithClosedChild(t *testing.T, bd, dir string) (root, child *types.Issue) {
	t.Helper()
	bdConfig(t, bd, dir, "set", "status.custom", "resolved:done")
	root = bdCreate(t, bd, dir, "root", "--type", "epic")
	child = bdCreate(t, bd, dir, "child", "--type", "task")
	bdDepAdd(t, bd, dir, child.ID, root.ID, "--type", "parent-child")
	bdClose(t, bd, dir, child.ID)
	bdUpdate(t, bd, dir, root.ID, "--status", "resolved")
	if got := bdShow(t, bd, dir, root.ID); got.Status != types.Status("resolved") {
		t.Fatalf("precondition: parent should be in custom done status 'resolved', got %q", got.Status)
	}
	return root, child
}
