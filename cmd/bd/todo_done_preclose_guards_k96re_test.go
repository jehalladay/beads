//go:build cgo

package main

import (
	"os/exec"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// beads-k96re: `bd todo done` is a documented convenience wrapper for `bd close`
// (and beads-58kg8 gave it the two POST-close steps), but it enforced NONE of
// the three PRE-close guards `bd close` runs before CloseIssue (close.go:158-203):
// open-child (auto-closing parent), gate-satisfaction, and open-blocker. So it
// silently closed a blocked issue and orphaned an open child under a closed
// parent — the exact states those guards forbid. The fix runs the same guard
// sequence per item, overridable with --force (mirroring `bd close --force`).
//
// MUTATION-VERIFIED: removing the guard block in todo.go → the "refuses"
// subtests go RED (todo done closes the blocked/parent issue).

// bdTodoDoneFail runs `bd todo done <args>` expecting a non-zero exit and
// returns combined output.
func bdTodoDoneFail(t *testing.T, bd, dir string, args ...string) string {
	t.Helper()
	full := append([]string{"todo", "done"}, args...)
	cmd := exec.Command(bd, full...)
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("`bd todo done %v` succeeded but expected a guard refusal (exit non-zero)\noutput:\n%s", args, out)
	}
	return string(out)
}

func TestTodoDone_RefusesBlockedIssue_k96re(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "tgb")

	blocker := bdCreate(t, bd, dir, "blocker", "--type", "task")
	blocked := bdCreate(t, bd, dir, "blocked", "--type", "task")
	bdDepAdd(t, bd, dir, blocked.ID, blocker.ID) // blocked depends on (open) blocker

	// Without --force, `bd todo done` must refuse (parity with `bd close`).
	bdTodoDoneFail(t, bd, dir, blocked.ID)
	if got := bdShow(t, bd, dir, blocked.ID); got.Status == types.StatusClosed {
		t.Errorf("`bd todo done` closed a BLOCKED issue %s without --force (beads-k96re) — pre-close blocker guard bypassed", blocked.ID)
	}

	// --force overrides, mirroring `bd close --force`.
	fcmd := exec.Command(bd, "todo", "done", blocked.ID, "--force")
	fcmd.Dir = dir
	fcmd.Env = bdEnv(dir)
	if out, err := fcmd.CombinedOutput(); err != nil {
		t.Fatalf("`bd todo done --force` on a blocked issue failed: %v\n%s", err, out)
	}
	if got := bdShow(t, bd, dir, blocked.ID); got.Status != types.StatusClosed {
		t.Errorf("`bd todo done --force` did not close blocked issue %s (status=%s)", blocked.ID, got.Status)
	}
}

func TestTodoDone_RefusesParentWithOpenChild_k96re(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "tgc")

	epic := bdCreate(t, bd, dir, "epic parent", "--type", "epic")
	child := bdCreate(t, bd, dir, "open child", "--type", "task")
	bdDep(t, bd, dir, "add", child.ID, epic.ID, "--type", "parent-child")

	// Without --force, closing the epic while a child is open must be refused
	// (the closed-parent-with-open-child invariant, close.go:168-174).
	bdTodoDoneFail(t, bd, dir, epic.ID)
	if got := bdShow(t, bd, dir, epic.ID); got.Status == types.StatusClosed {
		t.Errorf("`bd todo done` closed epic %s with an OPEN child without --force (beads-k96re) — open-child guard bypassed, orphaning the child", epic.ID)
	}

	// --force overrides.
	fcmd := exec.Command(bd, "todo", "done", epic.ID, "--force")
	fcmd.Dir = dir
	fcmd.Env = bdEnv(dir)
	if out, err := fcmd.CombinedOutput(); err != nil {
		t.Fatalf("`bd todo done --force` on a parent with open child failed: %v\n%s", err, out)
	}
	if got := bdShow(t, bd, dir, epic.ID); got.Status != types.StatusClosed {
		t.Errorf("`bd todo done --force` did not close epic %s (status=%s)", epic.ID, got.Status)
	}
}

// Positive: an unblocked, childless task still closes normally (the guards do
// not over-refuse).
func TestTodoDone_UnblockedStillCloses_k96re(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "tgu")

	task := bdCreate(t, bd, dir, "plain task", "--type", "task")
	cmd := exec.Command(bd, "todo", "done", task.ID)
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("`bd todo done` on a plain task failed: %v\n%s", err, out)
	}
	if got := bdShow(t, bd, dir, task.ID); got.Status != types.StatusClosed {
		t.Errorf("`bd todo done` did not close plain task %s (status=%s) — guards over-refused", task.ID, got.Status)
	}
}
