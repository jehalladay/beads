//go:build cgo

package main

import (
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/types"
)

// TestEpicCloseEligibleDoneCategory_g29sj is the beads-g29sj teeth for the epic
// close-eligibility straggler of the done-category-terminal vein
// (beads-bobpm/x463g). GetEpicsEligibleForClosureInTx counts a child toward
// ClosedChildren / EligibleForClose only on the LITERAL types.StatusClosed:
//
//	if status == types.StatusClosed { closedChildren++ }
//
// A child moved to a custom done-category status (e.g. `status.custom
// "verified:done"` → CategoryDone) is a TERMINAL "done" outcome but is NOT
// literally 'closed', so it was NOT counted — leaving an epic whose children are
// ALL done-category reporting EligibleForClose:false and thus never
// auto-closing. bobpm/x463g made molecule progress, autoclose, and
// ready/count/list done-category-aware, but epic close-eligibility was the
// straggler. The fix folds the done-set into the completion test at the single
// shared chokepoint (epic_closure.go), matching molecule.go's counting:
//
//	case status == StatusClosed || doneStatusNames[status]: closedChildren++
//
// An empty done-set (no config / resolution error) reduces to byte-identical
// literal-'closed' behavior (degraded-safe).
//
// MUTATION-VERIFY: revert the guard to a bare `== types.StatusClosed` → the
// done-category subtest goes RED (ClosedChildren short by one,
// EligibleForClose:false) while the literal-closed and mixed controls stay green.
func TestEpicCloseEligibleDoneCategory_g29sj(t *testing.T) {
	h := newEpicTestHelper(t)

	// Register a custom done-category status on this store, mirroring the
	// ei6vq/ulsg4 done-category tests.
	if err := h.s.SetConfig(h.ctx, "status.custom", "verified:done"); err != nil {
		t.Fatalf("register done-category status: %v", err)
	}
	store = h.s

	// (1) All children in a done-category custom status → epic IS eligible.
	t.Run("AllDoneCategoryChildrenEligible", func(t *testing.T) {
		epic := &types.Issue{
			ID:        "epic-done-cat",
			Title:     "Epic with done-category children",
			Status:    types.StatusOpen,
			Priority:  1,
			IssueType: types.TypeEpic,
			CreatedAt: time.Now(),
		}
		h.createIssue(t, epic)

		for i, id := range []string{"g29-dc-1", "g29-dc-2"} {
			child := &types.Issue{
				ID:        id,
				Title:     "done-cat child",
				Status:    types.StatusOpen,
				Priority:  2,
				IssueType: types.TypeTask,
				CreatedAt: time.Now(),
			}
			h.createIssue(t, child)
			h.addDependency(t, &types.Dependency{
				IssueID:     child.ID,
				DependsOnID: epic.ID,
				Type:        types.DepParentChild,
			})
			// Move the child into the terminal done-category status.
			if err := h.s.UpdateIssue(h.ctx, child.ID, map[string]interface{}{"status": "verified"}, "test"); err != nil {
				t.Fatalf("move child %d to done-category status: %v", i, err)
			}
		}

		es := h.getEpicStatus(t, "epic-done-cat")
		if es == nil {
			t.Fatal("epic-done-cat not found in results")
		}
		if es.TotalChildren != 2 {
			t.Errorf("expected 2 total children, got %d", es.TotalChildren)
		}
		if es.ClosedChildren != 2 {
			t.Errorf("done-category children must count as closed: expected ClosedChildren=2, got %d", es.ClosedChildren)
		}
		if !es.EligibleForClose {
			t.Error("an epic whose children are ALL done-category must be eligible for close (beads-g29sj)")
		}
	})

	// (2) Mixed: one done-category, one still OPEN → NOT eligible (control that
	//     the done-aware count still requires ALL children terminal).
	t.Run("MixedDoneCategoryAndOpenNotEligible", func(t *testing.T) {
		epic := &types.Issue{
			ID:        "epic-mixed-dc",
			Title:     "Epic mixed done-category + open",
			Status:    types.StatusOpen,
			Priority:  1,
			IssueType: types.TypeEpic,
			CreatedAt: time.Now(),
		}
		h.createIssue(t, epic)

		done := &types.Issue{ID: "g29-mx-done", Title: "done", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask, CreatedAt: time.Now()}
		open := &types.Issue{ID: "g29-mx-open", Title: "open", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask, CreatedAt: time.Now()}
		h.createIssue(t, done)
		h.createIssue(t, open)
		for _, c := range []*types.Issue{done, open} {
			h.addDependency(t, &types.Dependency{IssueID: c.ID, DependsOnID: epic.ID, Type: types.DepParentChild})
		}
		if err := h.s.UpdateIssue(h.ctx, done.ID, map[string]interface{}{"status": "verified"}, "test"); err != nil {
			t.Fatalf("move child to done-category status: %v", err)
		}

		es := h.getEpicStatus(t, "epic-mixed-dc")
		if es == nil {
			t.Fatal("epic-mixed-dc not found in results")
		}
		if es.TotalChildren != 2 {
			t.Errorf("expected 2 total children, got %d", es.TotalChildren)
		}
		if es.ClosedChildren != 1 {
			t.Errorf("exactly one child (the done-category one) must count as closed: expected 1, got %d", es.ClosedChildren)
		}
		if es.EligibleForClose {
			t.Error("epic with one still-OPEN child must NOT be eligible for close")
		}
	})

	// (3) Regression control: literal-closed children still count with a done-set
	//     registered (the done-aware count is a strict superset).
	t.Run("LiteralClosedChildStillCountsWithDoneSet", func(t *testing.T) {
		epic := &types.Issue{
			ID:        "epic-lit-closed",
			Title:     "Epic literal-closed child",
			Status:    types.StatusOpen,
			Priority:  1,
			IssueType: types.TypeEpic,
			CreatedAt: time.Now(),
		}
		h.createIssue(t, epic)
		child := &types.Issue{
			ID:        "g29-lit-1",
			Title:     "literal closed child",
			Status:    types.StatusClosed,
			Priority:  2,
			IssueType: types.TypeTask,
			CreatedAt: time.Now(),
			ClosedAt:  ptrTime(time.Now()),
		}
		h.createIssue(t, child)
		h.addDependency(t, &types.Dependency{IssueID: child.ID, DependsOnID: epic.ID, Type: types.DepParentChild})

		es := h.getEpicStatus(t, "epic-lit-closed")
		if es == nil {
			t.Fatal("epic-lit-closed not found in results")
		}
		if es.ClosedChildren != 1 || !es.EligibleForClose {
			t.Errorf("literal-closed child must still count with a done-set present: ClosedChildren=%d EligibleForClose=%v", es.ClosedChildren, es.EligibleForClose)
		}
	})
}
