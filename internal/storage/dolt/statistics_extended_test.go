package dolt

import (
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestGetStatistics_EpicsEligibleAndLeadTime is the behavioral teeth for
// beads-13xl: bd stats declared + rendered EpicsEligibleForClosure and
// AverageLeadTime but no GetStatistics path ever assigned them → permanent 0,
// disagreeing with bd epic status. This runs the real embedded store end-to-end
// (a pure test would false-green: it cannot prove TIMESTAMPDIFF is supported by
// the go-mysql-server engine nor that the epic-eligible count is wired).
func TestGetStatistics_EpicsEligibleAndLeadTime(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()
	ctx, cancel := testContext(t)
	defer cancel()

	// An epic with a single child; close the child → epic becomes eligible.
	epic := &types.Issue{ID: "se-epic", Title: "Epic", Status: types.StatusOpen, Priority: 1, IssueType: types.TypeEpic}
	child := &types.Issue{ID: "se-child", Title: "Child", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask}
	if err := store.CreateIssue(ctx, epic, "tester"); err != nil {
		t.Fatalf("create epic: %v", err)
	}
	if err := store.CreateIssue(ctx, child, "tester"); err != nil {
		t.Fatalf("create child: %v", err)
	}
	if err := store.AddDependency(ctx, &types.Dependency{
		IssueID: "se-child", DependsOnID: "se-epic", Type: types.DepParentChild,
	}, "tester"); err != nil {
		t.Fatalf("AddDependency parent-child: %v", err)
	}

	// Before closing the child: epic not eligible.
	stats, err := store.GetStatistics(ctx)
	if err != nil {
		t.Fatalf("GetStatistics (pre-close): %v", err)
	}
	if stats.EpicsEligibleForClosure != 0 {
		t.Errorf("pre-close EpicsEligibleForClosure = %d, want 0", stats.EpicsEligibleForClosure)
	}

	if err := store.CloseIssue(ctx, "se-child", "done", "tester", ""); err != nil {
		t.Fatalf("CloseIssue child: %v", err)
	}

	stats, err = store.GetStatistics(ctx)
	if err != nil {
		t.Fatalf("GetStatistics (post-close): %v", err)
	}

	// The stats field must agree with the canonical bd epic status helper.
	eligible, err := store.GetEpicsEligibleForClosure(ctx)
	if err != nil {
		t.Fatalf("GetEpicsEligibleForClosure: %v", err)
	}
	wantEligible := 0
	for _, e := range eligible {
		if e.EligibleForClose {
			wantEligible++
		}
	}
	if wantEligible != 1 {
		t.Fatalf("test setup: expected 1 eligible epic from canonical helper, got %d", wantEligible)
	}
	if stats.EpicsEligibleForClosure != wantEligible {
		t.Errorf("EpicsEligibleForClosure = %d, want %d (must agree with bd epic status)",
			stats.EpicsEligibleForClosure, wantEligible)
	}

	// AverageLeadTime must compute without engine error (proves TIMESTAMPDIFF is
	// supported by embedded Dolt) and be a non-negative real number. There is at
	// least one closed issue (se-child), so the AVG query runs over a real row;
	// a just-closed issue yields ~0 hours, so assert >= 0 (not > 0).
	if stats.AverageLeadTime < 0 {
		t.Errorf("AverageLeadTime = %v, want >= 0 (computed, not the old permanent-0 bug's error path)", stats.AverageLeadTime)
	}
}
