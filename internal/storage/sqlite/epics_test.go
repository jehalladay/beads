package sqlite

import (
	"context"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

func TestGetEpicsEligibleForClosure(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Create an epic
	epic := &types.Issue{
		Title:       "Test Epic",
		Description: "Epic for testing",
		Status:      types.StatusOpen,
		Priority:    1,
		IssueType:   types.TypeEpic,
	}
	err := store.CreateIssue(ctx, epic, "test-user")
	if err != nil {
		t.Fatalf("CreateIssue (epic) failed: %v", err)
	}

	// Create two child tasks
	task1 := &types.Issue{
		Title:     "Task 1",
		Status:    types.StatusOpen,
		Priority:  2,
		IssueType: types.TypeTask,
	}
	err = store.CreateIssue(ctx, task1, "test-user")
	if err != nil {
		t.Fatalf("CreateIssue (task1) failed: %v", err)
	}

	task2 := &types.Issue{
		Title:     "Task 2",
		Status:    types.StatusOpen,
		Priority:  2,
		IssueType: types.TypeTask,
	}
	err = store.CreateIssue(ctx, task2, "test-user")
	if err != nil {
		t.Fatalf("CreateIssue (task2) failed: %v", err)
	}

	// Add parent-child dependencies
	dep1 := &types.Dependency{
		IssueID:     task1.ID,
		DependsOnID: epic.ID,
		Type:        types.DepParentChild,
	}
	err = store.AddDependency(ctx, dep1, "test-user")
	if err != nil {
		t.Fatalf("AddDependency (task1) failed: %v", err)
	}

	dep2 := &types.Dependency{
		IssueID:     task2.ID,
		DependsOnID: epic.ID,
		Type:        types.DepParentChild,
	}
	err = store.AddDependency(ctx, dep2, "test-user")
	if err != nil {
		t.Fatalf("AddDependency (task2) failed: %v", err)
	}

	// Test 1: Epic with open children should NOT be eligible for closure
	epics, err := store.GetEpicsEligibleForClosure(ctx)
	if err != nil {
		t.Fatalf("GetEpicsEligibleForClosure failed: %v", err)
	}

	if len(epics) == 0 {
		t.Fatal("Expected at least one epic")
	}

	found := false
	for _, e := range epics {
		if e.Epic.ID == epic.ID {
			found = true
			if e.TotalChildren != 2 {
				t.Errorf("Expected 2 total children, got %d", e.TotalChildren)
			}
			if e.ClosedChildren != 0 {
				t.Errorf("Expected 0 closed children, got %d", e.ClosedChildren)
			}
			if e.EligibleForClose {
				t.Error("Epic should NOT be eligible for closure with open children")
			}
		}
	}
	if !found {
		t.Error("Epic not found in results")
	}

	// Test 2: Close one task
	err = store.CloseIssue(ctx, task1.ID, "Done", "test-user")
	if err != nil {
		t.Fatalf("CloseIssue (task1) failed: %v", err)
	}

	epics, err = store.GetEpicsEligibleForClosure(ctx)
	if err != nil {
		t.Fatalf("GetEpicsEligibleForClosure (after closing task1) failed: %v", err)
	}

	found = false
	for _, e := range epics {
		if e.Epic.ID == epic.ID {
			found = true
			if e.ClosedChildren != 1 {
				t.Errorf("Expected 1 closed child, got %d", e.ClosedChildren)
			}
			if e.EligibleForClose {
				t.Error("Epic should NOT be eligible with only 1/2 tasks closed")
			}
		}
	}
	if !found {
		t.Error("Epic not found after closing one task")
	}

	// Test 3: Close second task - epic should be eligible
	err = store.CloseIssue(ctx, task2.ID, "Done", "test-user")
	if err != nil {
		t.Fatalf("CloseIssue (task2) failed: %v", err)
	}

	epics, err = store.GetEpicsEligibleForClosure(ctx)
	if err != nil {
		t.Fatalf("GetEpicsEligibleForClosure (after closing task2) failed: %v", err)
	}

	found = false
	for _, e := range epics {
		if e.Epic.ID == epic.ID {
			found = true
			if e.ClosedChildren != 2 {
				t.Errorf("Expected 2 closed children, got %d", e.ClosedChildren)
			}
			if !e.EligibleForClose {
				t.Error("Epic SHOULD be eligible for closure with all children closed")
			}
		}
	}
	if !found {
		t.Error("Epic not found after closing all tasks")
	}

	// Test 4: Close the epic - should no longer appear in results
	err = store.CloseIssue(ctx, epic.ID, "All tasks complete", "test-user")
	if err != nil {
		t.Fatalf("CloseIssue (epic) failed: %v", err)
	}

	epics, err = store.GetEpicsEligibleForClosure(ctx)
	if err != nil {
		t.Fatalf("GetEpicsEligibleForClosure (after closing epic) failed: %v", err)
	}

	// Closed epics should not appear in results
	for _, e := range epics {
		if e.Epic.ID == epic.ID {
			t.Error("Closed epic should not appear in eligible list")
		}
	}
}

func TestGetEpicsEligibleForClosureWithNoChildren(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Create an epic with no children
	epic := &types.Issue{
		Title:     "Childless Epic",
		Status:    types.StatusOpen,
		Priority:  1,
		IssueType: types.TypeEpic,
	}
	err := store.CreateIssue(ctx, epic, "test-user")
	if err != nil {
		t.Fatalf("CreateIssue failed: %v", err)
	}

	epics, err := store.GetEpicsEligibleForClosure(ctx)
	if err != nil {
		t.Fatalf("GetEpicsEligibleForClosure failed: %v", err)
	}

	// Should find the epic but it should NOT be eligible (no children = not eligible)
	found := false
	for _, e := range epics {
		if e.Epic.ID == epic.ID {
			found = true
			if e.TotalChildren != 0 {
				t.Errorf("Expected 0 total children, got %d", e.TotalChildren)
			}
			if e.EligibleForClose {
				t.Error("Epic with no children should NOT be eligible for closure")
			}
		}
	}
	if !found {
		t.Error("Epic not found in results")
	}
}
