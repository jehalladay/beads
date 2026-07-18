//go:build cgo

package dolt

import (
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// beads-a3hm: conditional-blocks means "B runs only if A FAILS". Closing the
// target A must therefore be reason-aware in BOTH the authoritative is_blocked
// recompute AND the --suggest-next display (GetNewlyUnblockedByClose), which
// previously disagreed: recompute unblocked B on ANY close (reason-blind) while
// the display omitted conditional-blocks entirely. These tests pin the ruled
// contract for both close reasons on both paths.

// --- recompute (is_blocked) path ---

// A FAILURE close of the target satisfies "runs only if A fails" → B is
// genuinely unblocked, so is_blocked must clear.
func TestConditionalBlocks_FailureClose_Unblocks(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()
	ctx, cancel := testContext(t)
	defer cancel()

	createPerm(t, ctx, store, "cb-fail-target")
	createPerm(t, ctx, store, "cb-fail-source")
	if err := store.AddDependency(ctx, &types.Dependency{
		IssueID: "cb-fail-source", DependsOnID: "cb-fail-target", Type: types.DepConditionalBlocks,
	}, "tester"); err != nil {
		t.Fatalf("AddDependency conditional-blocks: %v", err)
	}
	if !getIsBlocked(t, ctx, store, "issues", "cb-fail-source") {
		t.Fatal("precondition: source should be is_blocked while target open")
	}

	if err := store.CloseIssue(ctx, "cb-fail-target", "failed: could not repro", "tester", ""); err != nil {
		t.Fatalf("CloseIssue (failure): %v", err)
	}
	if getIsBlocked(t, ctx, store, "issues", "cb-fail-source") {
		t.Error("failure-close of conditional-blocks target should UNBLOCK the source (condition met)")
	}
}

// A SUCCESS close of the target means "runs only if A fails" can never be met →
// B must STAY blocked. This is the bug: the old recompute unblocked reason-blind.
func TestConditionalBlocks_SuccessClose_StaysBlocked(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()
	ctx, cancel := testContext(t)
	defer cancel()

	createPerm(t, ctx, store, "cb-ok-target")
	createPerm(t, ctx, store, "cb-ok-source")
	if err := store.AddDependency(ctx, &types.Dependency{
		IssueID: "cb-ok-source", DependsOnID: "cb-ok-target", Type: types.DepConditionalBlocks,
	}, "tester"); err != nil {
		t.Fatalf("AddDependency conditional-blocks: %v", err)
	}
	if !getIsBlocked(t, ctx, store, "issues", "cb-ok-source") {
		t.Fatal("precondition: source should be is_blocked while target open")
	}

	if err := store.CloseIssue(ctx, "cb-ok-target", "done", "tester", ""); err != nil {
		t.Fatalf("CloseIssue (success): %v", err)
	}
	if !getIsBlocked(t, ctx, store, "issues", "cb-ok-source") {
		t.Error("success-close of conditional-blocks target must KEEP the source blocked (condition never met)")
	}
}

// A plain 'blocks' edge is reason-independent: any close unblocks. Guards that
// the reason-aware conditional-blocks change did not regress hard blocks.
func TestBlocks_SuccessClose_Unblocks(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()
	ctx, cancel := testContext(t)
	defer cancel()

	createPerm(t, ctx, store, "hb-ok-target")
	createPerm(t, ctx, store, "hb-ok-source")
	if err := store.AddDependency(ctx, &types.Dependency{
		IssueID: "hb-ok-source", DependsOnID: "hb-ok-target", Type: types.DepBlocks,
	}, "tester"); err != nil {
		t.Fatalf("AddDependency blocks: %v", err)
	}
	if !getIsBlocked(t, ctx, store, "issues", "hb-ok-source") {
		t.Fatal("precondition: source should be is_blocked while target open")
	}

	if err := store.CloseIssue(ctx, "hb-ok-target", "done", "tester", ""); err != nil {
		t.Fatalf("CloseIssue (success): %v", err)
	}
	if getIsBlocked(t, ctx, store, "issues", "hb-ok-source") {
		t.Error("success-close of a plain blocks target should UNBLOCK the source (reason-independent)")
	}
}

// --- display (GetNewlyUnblockedByClose / --suggest-next) path ---

// On a FAILURE close, the conditional-blocks dependent is genuinely unblocked,
// so the display must LIST it. The old display omitted conditional-blocks.
func TestGetNewlyUnblockedByClose_ConditionalBlocks_FailureCloseListsIt(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()
	ctx, cancel := testContext(t)
	defer cancel()

	createPerm(t, ctx, store, "cbd-fail-target")
	createPerm(t, ctx, store, "cbd-fail-source")
	if err := store.AddDependency(ctx, &types.Dependency{
		IssueID: "cbd-fail-source", DependsOnID: "cbd-fail-target", Type: types.DepConditionalBlocks,
	}, "tester"); err != nil {
		t.Fatalf("AddDependency conditional-blocks: %v", err)
	}

	if err := store.CloseIssue(ctx, "cbd-fail-target", "rejected", "tester", ""); err != nil {
		t.Fatalf("CloseIssue (failure): %v", err)
	}
	unblocked, err := store.GetNewlyUnblockedByClose(ctx, "cbd-fail-target")
	if err != nil {
		t.Fatalf("GetNewlyUnblockedByClose: %v", err)
	}
	if !containsID(issueIDs(unblocked), "cbd-fail-source") {
		t.Errorf("failure-close: display should list conditional-blocks dependent, got %v", issueIDs(unblocked))
	}
}

// On a SUCCESS close, the conditional-blocks dependent can never run → NOT newly
// unblocked, so the display must NOT list it (matches recompute keeping it blocked).
func TestGetNewlyUnblockedByClose_ConditionalBlocks_SuccessCloseOmitsIt(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()
	ctx, cancel := testContext(t)
	defer cancel()

	createPerm(t, ctx, store, "cbd-ok-target")
	createPerm(t, ctx, store, "cbd-ok-source")
	if err := store.AddDependency(ctx, &types.Dependency{
		IssueID: "cbd-ok-source", DependsOnID: "cbd-ok-target", Type: types.DepConditionalBlocks,
	}, "tester"); err != nil {
		t.Fatalf("AddDependency conditional-blocks: %v", err)
	}

	if err := store.CloseIssue(ctx, "cbd-ok-target", "done", "tester", ""); err != nil {
		t.Fatalf("CloseIssue (success): %v", err)
	}
	unblocked, err := store.GetNewlyUnblockedByClose(ctx, "cbd-ok-target")
	if err != nil {
		t.Fatalf("GetNewlyUnblockedByClose: %v", err)
	}
	if containsID(issueIDs(unblocked), "cbd-ok-source") {
		t.Errorf("success-close: display must NOT list a conditional-blocks dependent that can never run, got %v", issueIDs(unblocked))
	}
}

// A candidate blocked by BOTH a just-failed conditional-blocks edge AND a still-open
// plain blocker must stay OFF the newly-unblocked list (remaining-blocker check
// must see the plain blocker), proving the reason-aware remaining check composes.
func TestGetNewlyUnblockedByClose_ConditionalBlocks_RemainingHardBlockerHolds(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()
	ctx, cancel := testContext(t)
	defer cancel()

	createPerm(t, ctx, store, "cbr-cond-target")
	createPerm(t, ctx, store, "cbr-hard-target")
	createPerm(t, ctx, store, "cbr-source")
	if err := store.AddDependency(ctx, &types.Dependency{
		IssueID: "cbr-source", DependsOnID: "cbr-cond-target", Type: types.DepConditionalBlocks,
	}, "tester"); err != nil {
		t.Fatalf("AddDependency conditional-blocks: %v", err)
	}
	if err := store.AddDependency(ctx, &types.Dependency{
		IssueID: "cbr-source", DependsOnID: "cbr-hard-target", Type: types.DepBlocks,
	}, "tester"); err != nil {
		t.Fatalf("AddDependency blocks: %v", err)
	}

	if err := store.CloseIssue(ctx, "cbr-cond-target", "failed", "tester", ""); err != nil {
		t.Fatalf("CloseIssue (failure): %v", err)
	}
	unblocked, err := store.GetNewlyUnblockedByClose(ctx, "cbr-cond-target")
	if err != nil {
		t.Fatalf("GetNewlyUnblockedByClose: %v", err)
	}
	if containsID(issueIDs(unblocked), "cbr-source") {
		t.Errorf("source still has an open hard blocker; must not be newly unblocked, got %v", issueIDs(unblocked))
	}
}
