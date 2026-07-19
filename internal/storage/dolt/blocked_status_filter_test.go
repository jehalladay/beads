package dolt

import (
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestBlockedFilter_SearchAndCount is the behavioral teeth for beads-7f3g: a
// blocked issue keeps stored status=open (is_blocked is a derived column), so
// the OLD `--status blocked` path (filter.Status="blocked" against the status
// column) always returned 0. The fix routes `--status blocked` to
// IssueFilter.Blocked (is_blocked), so SearchIssues/CountIssues now agree with
// bd blocked. This exercises the real embedded store end-to-end (a pure builder
// test would false-green — it can't prove the WHERE actually matches rows).
func TestBlockedFilter_SearchAndCount(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()
	ctx, cancel := testContext(t)
	defer cancel()

	createPerm(t, ctx, store, "bsf-blocker")
	createPerm(t, ctx, store, "bsf-blocked")

	if err := store.AddDependency(ctx, &types.Dependency{
		IssueID: "bsf-blocked", DependsOnID: "bsf-blocker", Type: types.DepBlocks,
	}, "tester"); err != nil {
		t.Fatalf("AddDependency: %v", err)
	}

	// Sanity: the blocked issue keeps stored status=open — so the buggy path
	// (Status="blocked" against the status column) can never match it.
	blockedTrue := true
	got, err := store.SearchIssues(ctx, "", types.IssueFilter{Blocked: &blockedTrue})
	if err != nil {
		t.Fatalf("SearchIssues(Blocked=true): %v", err)
	}
	if len(got) != 1 || got[0].ID != "bsf-blocked" {
		ids := make([]string, len(got))
		for i, g := range got {
			ids[i] = g.ID
		}
		t.Fatalf("Blocked=true search = %v, want exactly [bsf-blocked]", ids)
	}

	n, err := store.CountIssues(ctx, "", types.IssueFilter{Blocked: &blockedTrue})
	if err != nil {
		t.Fatalf("CountIssues(Blocked=true): %v", err)
	}
	if n != 1 {
		t.Fatalf("CountIssues(Blocked=true) = %d, want 1", n)
	}

	// Cross-check: the OLD behavior (matching the literal "blocked" against the
	// status column) yields 0 — this is exactly the silent bug the fix routes
	// around, and pins that the two paths genuinely differ.
	badStatus := types.Status("blocked")
	nBad, err := store.CountIssues(ctx, "", types.IssueFilter{Status: &badStatus})
	if err != nil {
		t.Fatalf("CountIssues(Status=blocked): %v", err)
	}
	if nBad != 0 {
		t.Fatalf("status-column 'blocked' should match nothing (proves the bug), got %d", nBad)
	}

	// Blocked=false must exclude the blocked issue but include the blocker.
	blockedFalse := false
	unblocked, err := store.SearchIssues(ctx, "", types.IssueFilter{Blocked: &blockedFalse})
	if err != nil {
		t.Fatalf("SearchIssues(Blocked=false): %v", err)
	}
	sawBlocker, sawBlocked := false, false
	for _, u := range unblocked {
		switch u.ID {
		case "bsf-blocker":
			sawBlocker = true
		case "bsf-blocked":
			sawBlocked = true
		}
	}
	if !sawBlocker {
		t.Errorf("Blocked=false should include the unblocked blocker bsf-blocker")
	}
	if sawBlocked {
		t.Errorf("Blocked=false must NOT include the blocked issue bsf-blocked")
	}
}
