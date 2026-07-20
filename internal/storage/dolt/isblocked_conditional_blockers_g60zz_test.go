package dolt

import (
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestIsBlocked_ConditionalBlocksSuccessCloseStaysInBlockersList is the teeth for
// beads-g60zz (sibling of mxe4b): store.IsBlocked -> IsBlockedInTx returned the
// dependent as blocked=true (correct, from the is_blocked column) but built its
// blockers list with a naive "target closed -> skip" check, so a SUCCESS-closed
// conditional-blocks blocker was DROPPED from the list. The close/update
// blocked-guards (close.go / update.go / batch.go) gate on
// `blocked && len(blockers) > 0`, so blocked=true with an empty list let a
// genuinely-blocked issue close WITHOUT --force — a guard BYPASS, not just a
// display gap. A conditional-blocks edge ("B runs only if A FAILS") still holds B
// while A is closed non-failure.
//
// Locks: after a SUCCESS close of a conditional-blocks blocker, the dependent
// stays blocked=true AND the blocker stays in the returned list; after a FAILURE
// close it drops from both (the condition is satisfied -> runnable).
func TestIsBlocked_ConditionalBlocksSuccessCloseStaysInBlockersList(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx, cancel := testContext(t)
	defer cancel()

	blockerA := &types.Issue{
		ID:        "icb-a",
		Title:     "Conditional blocker A",
		Status:    types.StatusOpen,
		Priority:  1,
		IssueType: types.TypeTask,
	}
	dependentB := &types.Issue{
		ID:        "icb-b",
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

	// While A is OPEN, B is blocked and A must be listed as its blocker.
	blocked, blockers, err := store.IsBlocked(ctx, dependentB.ID)
	if err != nil {
		t.Fatalf("IsBlocked (A open): %v", err)
	}
	if !blocked || len(blockers) == 0 {
		t.Fatalf("with blocker A open, %s must be blocked with a non-empty blockers list (got blocked=%v blockers=%v)", dependentB.ID, blocked, blockers)
	}

	// SUCCESS close of A: the conditional edge still holds B blocked. is_blocked
	// stays 1; the bug dropped A from the blockers list, so the close-guard
	// (blocked && len(blockers)>0) evaluated false and B was closable w/o --force.
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
		t.Fatalf("%s must still be blocked=true after SUCCESS close of a conditional-blocks blocker", dependentB.ID)
	}
	if len(blockers) == 0 {
		t.Errorf("beads-g60zz: after SUCCESS close of conditional-blocks blocker, %s is blocked=true but its blockers list is EMPTY — the close/update guard (blocked && len(blockers)>0) would let it close without --force", dependentB.ID)
	}

	// FAILURE close of A satisfies B's condition -> B unblocked, absent from both.
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
	if blocked || len(blockers) > 0 {
		t.Errorf("after FAILURE close of conditional-blocks blocker, %s should be unblocked with an empty blockers list (got blocked=%v blockers=%v)", dependentB.ID, blocked, blockers)
	}
}
