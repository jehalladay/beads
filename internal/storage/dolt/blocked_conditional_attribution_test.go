package dolt

import (
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestGetBlockedIssues_ConditionalBlocksSuccessCloseStaysAttributed is the teeth
// for beads-mxe4b: `bd blocked` (store.GetBlockedIssues -> GetBlockedIssuesInTx)
// silently dropped a dependent whose conditional-blocks blocker had closed with a
// SUCCESS reason, even though the is_blocked recompute correctly keeps the
// dependent blocked (a conditional-blocks edge "B runs only if A FAILS" still
// holds B while A is closed with a non-failure reason — B can never run). The
// blocker-attribution loop used a naive "target closed -> skip" check instead of
// the reason-aware isActiveConditionalOrHardBlocker (beads-a3hm), so the
// dependent got no blockerMap entry and was dropped from the displayed results —
// a display-layer violation of the a3hm invariant the marking layer honors.
//
// This locks the invariant end-to-end: after a SUCCESS close of a
// conditional-blocks blocker, the dependent MUST still appear in
// GetBlockedIssues (matching is_blocked=1 / `bd list --status blocked`); after a
// FAILURE close it MUST drop out (the condition is satisfied -> runnable).
func TestGetBlockedIssues_ConditionalBlocksSuccessCloseStaysAttributed(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx, cancel := testContext(t)
	defer cancel()

	// A conditional-blocks B: "B runs only if A FAILS".
	blockerA := &types.Issue{
		ID:        "cba-a",
		Title:     "Conditional blocker A",
		Status:    types.StatusOpen,
		Priority:  1,
		IssueType: types.TypeTask,
	}
	dependentB := &types.Issue{
		ID:        "cba-b",
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

	blockedContains := func(t *testing.T, id string) bool {
		t.Helper()
		blocked, err := store.GetBlockedIssues(ctx, types.WorkFilter{})
		if err != nil {
			t.Fatalf("GetBlockedIssues: %v", err)
		}
		for _, b := range blocked {
			if b.Issue.ID == id {
				return true
			}
		}
		return false
	}

	// While A is OPEN, B is blocked and must be attributed/displayed.
	if !blockedContains(t, dependentB.ID) {
		t.Fatalf("with blocker A open, %s must appear in GetBlockedIssues", dependentB.ID)
	}

	// SUCCESS close of A: the conditional edge still holds B blocked (B never
	// runs). The bug: B was silently dropped from GetBlockedIssues here even
	// though is_blocked stays 1.
	if err := store.CloseIssue(ctx, blockerA.ID, "completed", "tester", ""); err != nil {
		t.Fatalf("success-close A: %v", err)
	}
	if !isBlocked(ctx, t, store.db, dependentB.ID) {
		t.Fatalf("precondition: %s must still be is_blocked=1 after SUCCESS close of a conditional-blocks blocker", dependentB.ID)
	}
	if !blockedContains(t, dependentB.ID) {
		t.Errorf("beads-mxe4b: after SUCCESS close of conditional-blocks blocker, %s is is_blocked=1 but silently missing from GetBlockedIssues (bd blocked disagrees with bd list --status blocked)", dependentB.ID)
	}

	// FAILURE close of A satisfies B's condition -> B becomes runnable and must
	// drop out of the blocked view (the inverse direction, guards over-broadening).
	if err := store.UpdateIssue(ctx, blockerA.ID, map[string]interface{}{
		"status": string(types.StatusOpen),
	}, "tester"); err != nil {
		t.Fatalf("reopen A: %v", err)
	}
	if err := store.CloseIssue(ctx, blockerA.ID, "failed: build broke", "tester", ""); err != nil {
		t.Fatalf("failure-close A: %v", err)
	}
	if blockedContains(t, dependentB.ID) {
		t.Errorf("after FAILURE close of conditional-blocks blocker, %s should be unblocked and absent from GetBlockedIssues", dependentB.ID)
	}
}
