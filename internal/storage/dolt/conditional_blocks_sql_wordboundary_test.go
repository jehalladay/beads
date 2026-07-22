package dolt

import (
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestConditionalBlocks_SQLWordBoundaryParity is the teeth for the SQL-mirror
// half of beads-cwaj5. cwaj5 changed the Go types.IsFailureClose from a naive
// substring match to a whole-WORD regex, so a SUCCESS close reason that merely
// embeds a failure keyword inside a larger word — e.g. "unblocked the pipeline"
// (contains "blocked"), "no errors found" (contains "error") — is NOT misread as
// a failure close. But the SQL mirror failureCloseSQLPredicate (which drives the
// is_blocked recompute in blocked_state.go) still used INSTR(...) > 0 = the old
// substring match. Its own doc claims "the two can never drift (beads-a3hm)".
//
// So on a conditional-blocks edge ("B runs only if A FAILS"), closing A with a
// SUCCESS reason like "unblocked the pipeline" made the SQL recompute wrongly
// treat it as a FAILURE close and RELEASE B (is_blocked -> 0), while the Go
// display layer (word-boundary) correctly keeps B blocked -> the exact a3hm
// lockstep drift cwaj5 was supposed to prevent, but only fixed on the Go side.
//
// This locks the SQL side end-to-end: a SUCCESS close whose reason contains a
// failure keyword only as a substring (not a whole word) must keep the
// conditional-blocks dependent is_blocked=1.
func TestConditionalBlocks_SQLWordBoundaryParity(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx, cancel := testContext(t)
	defer cancel()

	blockerA := &types.Issue{
		ID:        "cbwb-a",
		Title:     "Conditional blocker A",
		Status:    types.StatusOpen,
		Priority:  1,
		IssueType: types.TypeTask,
	}
	dependentB := &types.Issue{
		ID:        "cbwb-b",
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

	// Precondition: with A open, B is blocked.
	if !isBlocked(ctx, t, store.db, dependentB.ID) {
		t.Fatalf("precondition: %s must be is_blocked=1 while blocker A is open", dependentB.ID)
	}

	// These SUCCESS close reasons each embed a failure keyword ONLY as a
	// substring, never as a whole word. The Go IsFailureClose (word-boundary)
	// returns false for all of them, so they are SUCCESS closes -> a
	// conditional-blocks dependent must STAY blocked. The old SQL substring
	// INSTR would (wrongly) treat them as failures and release B.
	successReasonsWithEmbeddedKeyword := []string{
		"unblocked the pipeline", // contains "blocked" inside "unblocked"
		"no errors found",        // contains "error" inside "errors"
		"reworked the fix",       // no keyword at all (control), but ensure success stays blocked
	}

	for _, reason := range successReasonsWithEmbeddedKeyword {
		reason := reason
		t.Run(reason, func(t *testing.T) {
			// Sanity: this reason really is a SUCCESS close per the Go source of truth.
			if types.IsFailureClose(reason) {
				t.Fatalf("test bug: %q should be a SUCCESS close per Go IsFailureClose (word-boundary)", reason)
			}

			if err := store.CloseIssue(ctx, blockerA.ID, reason, "tester", ""); err != nil {
				t.Fatalf("success-close A with %q: %v", reason, err)
			}

			if !isBlocked(ctx, t, store.db, dependentB.ID) {
				t.Errorf("cwaj5-SQL: after SUCCESS close of conditional-blocks blocker A with reason %q "+
					"(failure keyword embedded only as a substring, not a whole word), %s must STAY "+
					"is_blocked=1 — the SQL failureCloseSQLPredicate substring-match wrongly released it, "+
					"drifting from the Go word-boundary IsFailureClose (beads-a3hm invariant)", reason, dependentB.ID)
			}

			// Reset A to open for the next sub-reason so B re-blocks cleanly.
			if err := store.UpdateIssue(ctx, blockerA.ID, map[string]interface{}{
				"status": string(types.StatusOpen),
			}, "tester"); err != nil {
				t.Fatalf("reopen A: %v", err)
			}
			if !isBlocked(ctx, t, store.db, dependentB.ID) {
				t.Fatalf("after reopening A, %s must be is_blocked=1 again", dependentB.ID)
			}
		})
	}

	// Inverse direction (guard against over-narrowing): a genuine WHOLE-WORD
	// failure close must still RELEASE B.
	if err := store.CloseIssue(ctx, blockerA.ID, "failed: build broke", "tester", ""); err != nil {
		t.Fatalf("failure-close A: %v", err)
	}
	if isBlocked(ctx, t, store.db, dependentB.ID) {
		t.Errorf("after a genuine whole-word FAILURE close of conditional-blocks blocker A, %s must be released (is_blocked=0)", dependentB.ID)
	}
}
