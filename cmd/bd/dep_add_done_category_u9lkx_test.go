//go:build cgo

package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestEmbeddedDepAddDoneCategory_u9lkx is the beads-u9lkx teeth: the dep-add
// closed-parent guard (beads-eth8/aw9x8) was done-category-BLIND on BOTH legs at
// the three DIRECT sites — single positional (dep.go), bulk `--file`
// (validateBulkDepEdges), and batch (guardBatchDepAdds). Every site keyed on the
// literal `parent.Status == StatusClosed && child.Status != StatusClosed`, while
// the rest of the closed-parent-with-open-child family (close.go via ulsg4,
// reopen.go via h7uhe, lint.go via ulsg4/97gmg) was made done-category-aware.
//
// Two symptoms on a custom done-category status (e.g.
// `bd config set status.custom "verified:done"` → CategoryDone):
//
//  1. PARENT leg = FALSE-NEGATIVE / guard bypass (the important one): adding an
//     OPEN child under a DONE-category parent slipped past the guard, silently
//     recreating the terminal-parent-with-open-child inconsistency the family
//     prevents and that `bd lint` now FLAGS (ulsg4). dep-add was the one
//     un-plugged mutation path to reach that state.
//  2. CHILD leg = FALSE-POSITIVE: adding a DONE-category (complete) child under a
//     closed parent was WRONGLY refused — the child IS complete.
//
// The fix threads the configured done-set through parentStatusIsTerminal /
// childCountsAsOpen (the shared family helpers, close.go) at all three sites.
// Degraded-safe: an empty done-set reduces to byte-identical literal-'closed'.
//
// MUTATION-VERIFY: revert any site's guard to the literal
// `parent.Status == types.StatusClosed && child.Status != types.StatusClosed` →
// the done-category-PARENT subtests go GREEN-when-they-should-RED (the bypass
// re-lands: the refuse becomes rc=0) and the done-category-CHILD subtests go RED
// (the legitimate edge is refused). The literal-closed negatives stay green.
func TestEmbeddedDepAddDoneCategory_u9lkx(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)

	edgeLine := func(from, to string) string {
		return fmt.Sprintf(`{"from":%q,"to":%q,"type":"parent-child"}`+"\n", from, to)
	}
	edgeExists := func(t *testing.T, dir, child, parent string) bool {
		t.Helper()
		for _, d := range showDeps(t, bd, dir, child) {
			if d.ID == parent {
				return true
			}
		}
		return false
	}
	runBatch := func(t *testing.T, dir, stdin string, extraArgs ...string) (combined string, err error) {
		t.Helper()
		args := append([]string{"batch"}, extraArgs...)
		cmd := exec.Command(bd, args...)
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		cmd.Stdin = strings.NewReader(stdin)
		stdout, stderr, e := runCommandBuffers(t, cmd)
		return stdout.String() + stderr.String(), e
	}

	// newDoneCategoryEpic registers a custom done-category status "verified" and
	// returns a childless epic parked in it (parked-then-verified: create → move
	// to the done-category status without ever hitting the close guard, since it
	// has no children). Terminal but NOT literally closed.
	newDoneCategoryEpic := func(t *testing.T, dir, prefix string) string {
		t.Helper()
		bdConfig(t, bd, dir, "set", "status.custom", "verified:done")
		epic := bdCreate(t, bd, dir, prefix+" done-cat epic", "--type", "epic")
		bdUpdate(t, bd, dir, epic.ID, "--status", "verified")
		if got := bdShow(t, bd, dir, epic.ID); got.Status != types.Status("verified") {
			t.Fatalf("setup: epic %s should be in done-category 'verified', got %s", epic.ID, got.Status)
		}
		return epic.ID
	}

	// ---- PARENT leg: done-category parent must be treated as terminal (the
	// bypass fix). ----

	// (1) SINGLE `bd dep add <open-child> <done-cat-epic>` must be REFUSED.
	t.Run("single_open_child_to_done_category_epic_refuses", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "u1")
		epic := newDoneCategoryEpic(t, dir, "u1")
		child := bdCreate(t, bd, dir, "u1 open child", "--type", "task")
		out := bdDepAddFail(t, bd, dir, child.ID, epic, "--type", "parent-child")
		if !strings.Contains(out, "closed parent") {
			t.Errorf("single dep add of an open child under a done-category epic must be refused, got:\n%s", out)
		}
		if edgeExists(t, dir, child.ID, epic) {
			t.Errorf("the edge must not have landed (done-category parent bypass)")
		}
	})

	// (2) BULK `--file` must refuse the same edge.
	t.Run("bulk_open_child_to_done_category_epic_refuses", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "u2")
		epic := newDoneCategoryEpic(t, dir, "u2")
		child := bdCreate(t, bd, dir, "u2 open child", "--type", "task")
		file := writeBulkFile(t, edgeLine(child.ID, epic))
		out := bdDepAddFail(t, bd, dir, "--file", file)
		if !strings.Contains(out, "closed parent") {
			t.Errorf("bulk dep add of an open child under a done-category epic must be refused, got:\n%s", out)
		}
		if edgeExists(t, dir, child.ID, epic) {
			t.Errorf("bulk: the edge must not have landed (done-category parent bypass)")
		}
	})

	// (3) BATCH must refuse the same edge (end-to-end through `bd batch`).
	t.Run("batch_open_child_to_done_category_epic_refuses", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "u3")
		epic := newDoneCategoryEpic(t, dir, "u3")
		child := bdCreate(t, bd, dir, "u3 open child", "--type", "task")
		combined, err := runBatch(t, dir, "dep add "+child.ID+" "+epic+" parent-child\n")
		if err == nil {
			t.Fatalf("batch dep add of an open child under a done-category epic must FAIL, got rc=0:\n%s", combined)
		}
		if !strings.Contains(combined, "closed parent") {
			t.Errorf("batch: expected a 'closed parent' guard error, got:\n%s", combined)
		}
		if edgeExists(t, dir, child.ID, epic) {
			t.Errorf("batch: the edge must not have landed (done-category parent bypass)")
		}
	})

	// ---- CHILD leg: a done-category (complete) child under a literally-closed
	// parent must be ALLOWED (the false-positive fix). ----

	// (4) SINGLE: a done-category child does not leave an OPEN child → allowed.
	t.Run("single_done_category_child_to_closed_epic_allowed", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "u4")
		bdConfig(t, bd, dir, "set", "status.custom", "verified:done")
		epic := bdCreate(t, bd, dir, "u4 epic", "--type", "epic")
		bdClose(t, bd, dir, epic.ID) // childless → clean close
		child := bdCreate(t, bd, dir, "u4 done child", "--type", "task")
		bdUpdate(t, bd, dir, child.ID, "--status", "verified")
		bdDepAdd(t, bd, dir, child.ID, epic.ID, "--type", "parent-child") // must NOT be refused
		if !edgeExists(t, dir, child.ID, epic.ID) {
			t.Errorf("a done-category (complete) child under a closed epic should attach, not be refused")
		}
	})

	// (5) BULK: same for the --file path.
	t.Run("bulk_done_category_child_to_closed_epic_allowed", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "u5")
		bdConfig(t, bd, dir, "set", "status.custom", "verified:done")
		epic := bdCreate(t, bd, dir, "u5 epic", "--type", "epic")
		bdClose(t, bd, dir, epic.ID)
		child := bdCreate(t, bd, dir, "u5 done child", "--type", "task")
		bdUpdate(t, bd, dir, child.ID, "--status", "verified")
		file := writeBulkFile(t, edgeLine(child.ID, epic.ID))
		bdDepAdd(t, bd, dir, "--file", file) // must succeed
		if !edgeExists(t, dir, child.ID, epic.ID) {
			t.Errorf("bulk: a done-category child under a closed epic should attach")
		}
	})

	// (6) BATCH: same for the batch path.
	t.Run("batch_done_category_child_to_closed_epic_allowed", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "u6")
		bdConfig(t, bd, dir, "set", "status.custom", "verified:done")
		epic := bdCreate(t, bd, dir, "u6 epic", "--type", "epic")
		bdClose(t, bd, dir, epic.ID)
		child := bdCreate(t, bd, dir, "u6 done child", "--type", "task")
		bdUpdate(t, bd, dir, child.ID, "--status", "verified")
		combined, err := runBatch(t, dir, "dep add "+child.ID+" "+epic.ID+" parent-child\n")
		if err != nil {
			t.Fatalf("batch: a done-category child under a closed epic should attach, got error: %v\n%s", err, combined)
		}
		if !edgeExists(t, dir, child.ID, epic.ID) {
			t.Errorf("batch: a done-category child under a closed epic should attach\n%s", combined)
		}
	})

	// ---- NEGATIVES (degraded-safe / no over-widening) ----

	// (N-a) literal-closed parent + open child STILL refused (regression control:
	// the degraded-safe path with a done-set present must not weaken the base
	// guard).
	t.Run("literal_closed_parent_open_child_still_refuses", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "un1")
		bdConfig(t, bd, dir, "set", "status.custom", "verified:done") // done-set present
		epic := bdCreate(t, bd, dir, "un1 epic", "--type", "epic")
		bdClose(t, bd, dir, epic.ID)
		child := bdCreate(t, bd, dir, "un1 open child", "--type", "task")
		out := bdDepAddFail(t, bd, dir, child.ID, epic.ID, "--type", "parent-child")
		if !strings.Contains(out, "closed parent") {
			t.Errorf("literal-closed parent + open child must still be refused, got:\n%s", out)
		}
	})

	// (N-b) FROZEN-category parent is NOT terminal (parked != done) → an open
	// child under a frozen-category parent must be ALLOWED (the guard must not
	// treat frozen as closed).
	t.Run("frozen_category_parent_open_child_allowed", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "un2")
		bdConfig(t, bd, dir, "set", "status.custom", "parked:frozen")
		epic := bdCreate(t, bd, dir, "un2 frozen epic", "--type", "epic")
		bdUpdate(t, bd, dir, epic.ID, "--status", "parked")
		child := bdCreate(t, bd, dir, "un2 open child", "--type", "task")
		bdDepAdd(t, bd, dir, child.ID, epic.ID, "--type", "parent-child") // frozen != terminal → allowed
		if !edgeExists(t, dir, child.ID, epic.ID) {
			t.Errorf("an open child under a FROZEN-category (parked, not done) parent should attach — frozen is not terminal")
		}
	})
}
