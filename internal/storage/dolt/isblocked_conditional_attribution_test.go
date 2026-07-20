package dolt

import (
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestIsBlocked_ConditionalBlocksSuccessCloseKeepsBlockerListed is the teeth for
// beads-htsxn: store.IsBlocked (-> issueops.IsBlockedInTx) returned
// blocked=true with an EMPTY blockers list after a conditional-blocks blocker
// closed with a SUCCESS reason. Consumers gate on `blocked && len(blockers) > 0`
// (cmd/bd/close.go:182, update.go, batch.go), so an empty list silently
// BYPASSED the blocked-guard — closing/updating a still-blocked issue WITHOUT
// --force. This is the guard-path sibling of beads-mxe4b (the display-only
// `bd blocked` view). A conditional-blocks edge means "the dependent runs only
// if the blocker FAILS", so a SUCCESS close leaves the dependent permanently
// blocked (is_blocked stays 1) — the blockers list must reflect that.
//
// This locks the invariant end-to-end: after a SUCCESS close of a
// conditional-blocks blocker, IsBlocked must report blocked=true AND a
// non-empty blockers list (guard fires); after a FAILURE close the condition is
// satisfied -> the dependent is runnable and the blockers list drops out.
func TestIsBlocked_ConditionalBlocksSuccessCloseKeepsBlockerListed(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx, cancel := testContext(t)
	defer cancel()

	// A conditional-blocks B: "B runs only if A FAILS".
	blockerA := &types.Issue{
		ID:        "hcb-a",
		Title:     "Conditional blocker A",
		Status:    types.StatusOpen,
		Priority:  1,
		IssueType: types.TypeTask,
	}
	dependentB := &types.Issue{
		ID:        "hcb-b",
		Title:     "Runs only if A fails",
		Status:    types.StatusOpen,
		Priority:  2,
		IssueType: types.TypeTask,
	}
	for _, issue := range []*types.Issue{blockerA, dependentB} {
		if err := store.CreateIssue(ctx, issue, "tester"); err != nil {
			t.Fatalf("create %s: %v", issue.ID, err)
		}
	}

	if err := store.AddDependency(ctx, &types.Dependency{
		IssueID:     dependentB.ID,
		DependsOnID: blockerA.ID,
		Type:        types.DepConditionalBlocks,
	}, "tester"); err != nil {
		t.Fatalf("add conditional-blocks dep: %v", err)
	}

	// While A is OPEN, B is blocked and the blocker must be listed.
	blocked, blockers, err := store.IsBlocked(ctx, dependentB.ID)
	if err != nil {
		t.Fatalf("IsBlocked (A open): %v", err)
	}
	if !blocked || len(blockers) == 0 {
		t.Fatalf("with blocker A open, %s must be blocked with a non-empty blockers list; got blocked=%v blockers=%v", dependentB.ID, blocked, blockers)
	}

	// SUCCESS close of A: the conditional edge still holds B blocked (B never
	// runs). The bug: blockers came back EMPTY here even though blocked stays
	// true, bypassing the `blocked && len(blockers) > 0` close-guard.
	if err := store.CloseIssue(ctx, blockerA.ID, "completed", "tester", ""); err != nil {
		t.Fatalf("success-close A: %v", err)
	}
	if !isBlocked(ctx, t, store.db, dependentB.ID) {
		t.Fatalf("precondition: %s must still be is_blocked=1 after SUCCESS close of a conditional-blocks blocker", dependentB.ID)
	}
	blocked, blockers, err = store.IsBlocked(ctx, dependentB.ID)
	if err != nil {
		t.Fatalf("IsBlocked (A success-closed): %v", err)
	}
	if !blocked {
		t.Errorf("beads-htsxn: %s must remain blocked after SUCCESS close of conditional-blocks blocker", dependentB.ID)
	}
	if len(blockers) == 0 {
		t.Errorf("beads-htsxn: after SUCCESS close of conditional-blocks blocker, %s is blocked=true but blockers list is EMPTY -> `blocked && len(blockers)>0` close-guard is bypassed (closes a blocked issue without --force)", dependentB.ID)
	}

	// FAILURE close of A satisfies B's condition -> B becomes runnable: IsBlocked
	// must report NOT blocked (the inverse direction, guards over-broadening).
	if err := store.UpdateIssue(ctx, blockerA.ID, map[string]interface{}{
		"status": string(types.StatusOpen),
	}, "tester"); err != nil {
		t.Fatalf("reopen A: %v", err)
	}
	if err := store.CloseIssue(ctx, blockerA.ID, "failed: build broke", "tester", ""); err != nil {
		t.Fatalf("failure-close A: %v", err)
	}
	blocked, blockers, err = store.IsBlocked(ctx, dependentB.ID)
	if err != nil {
		t.Fatalf("IsBlocked (A failure-closed): %v", err)
	}
	if blocked && len(blockers) > 0 {
		t.Errorf("after FAILURE close of conditional-blocks blocker, %s should be unblocked (condition satisfied); got blocked=%v blockers=%v", dependentB.ID, blocked, blockers)
	}
}
