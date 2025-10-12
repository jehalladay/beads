package sqlite

import (
	"context"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

func TestGetReadyWork(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Create issues:
	// bd-1: open, no dependencies → READY
	// bd-2: open, depends on bd-1 (open) → BLOCKED
	// bd-3: open, no dependencies → READY
	// bd-4: closed, no dependencies → NOT READY (closed)
	// bd-5: open, depends on bd-4 (closed) → READY (blocker is closed)

	issue1 := &types.Issue{Title: "Ready 1", Status: types.StatusOpen, Priority: 1, IssueType: types.TypeTask}
	issue2 := &types.Issue{Title: "Blocked", Status: types.StatusOpen, Priority: 1, IssueType: types.TypeTask}
	issue3 := &types.Issue{Title: "Ready 2", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask}
	issue4 := &types.Issue{Title: "Closed", Status: types.StatusClosed, Priority: 1, IssueType: types.TypeTask}
	issue5 := &types.Issue{Title: "Ready 3", Status: types.StatusOpen, Priority: 0, IssueType: types.TypeTask}

	store.CreateIssue(ctx, issue1, "test-user")
	store.CreateIssue(ctx, issue2, "test-user")
	store.CreateIssue(ctx, issue3, "test-user")
	store.CreateIssue(ctx, issue4, "test-user")
	store.CloseIssue(ctx, issue4.ID, "Done", "test-user")
	store.CreateIssue(ctx, issue5, "test-user")

	// Add dependencies
	store.AddDependency(ctx, &types.Dependency{IssueID: issue2.ID, DependsOnID: issue1.ID, Type: types.DepBlocks}, "test-user")
	store.AddDependency(ctx, &types.Dependency{IssueID: issue5.ID, DependsOnID: issue4.ID, Type: types.DepBlocks}, "test-user")

	// Get ready work
	ready, err := store.GetReadyWork(ctx, types.WorkFilter{Status: types.StatusOpen})
	if err != nil {
		t.Fatalf("GetReadyWork failed: %v", err)
	}

	// Should have 3 ready issues: bd-1, bd-3, bd-5
	if len(ready) != 3 {
		t.Fatalf("Expected 3 ready issues, got %d", len(ready))
	}

	// Verify ready issues
	readyIDs := make(map[string]bool)
	for _, issue := range ready {
		readyIDs[issue.ID] = true
	}

	if !readyIDs[issue1.ID] {
		t.Errorf("Expected %s to be ready", issue1.ID)
	}
	if !readyIDs[issue3.ID] {
		t.Errorf("Expected %s to be ready", issue3.ID)
	}
	if !readyIDs[issue5.ID] {
		t.Errorf("Expected %s to be ready", issue5.ID)
	}
	if readyIDs[issue2.ID] {
		t.Errorf("Expected %s to be blocked, but it was ready", issue2.ID)
	}
}

func TestGetReadyWorkPriorityOrder(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Create issues with different priorities
	issueP0 := &types.Issue{Title: "Highest", Status: types.StatusOpen, Priority: 0, IssueType: types.TypeTask}
	issueP2 := &types.Issue{Title: "Medium", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask}
	issueP1 := &types.Issue{Title: "High", Status: types.StatusOpen, Priority: 1, IssueType: types.TypeTask}

	store.CreateIssue(ctx, issueP2, "test-user")
	store.CreateIssue(ctx, issueP0, "test-user")
	store.CreateIssue(ctx, issueP1, "test-user")

	// Get ready work
	ready, err := store.GetReadyWork(ctx, types.WorkFilter{Status: types.StatusOpen})
	if err != nil {
		t.Fatalf("GetReadyWork failed: %v", err)
	}

	if len(ready) != 3 {
		t.Fatalf("Expected 3 ready issues, got %d", len(ready))
	}

	// Verify priority ordering (P0 first, then P1, then P2)
	if ready[0].Priority != 0 {
		t.Errorf("Expected first issue to be P0, got P%d", ready[0].Priority)
	}
	if ready[1].Priority != 1 {
		t.Errorf("Expected second issue to be P1, got P%d", ready[1].Priority)
	}
	if ready[2].Priority != 2 {
		t.Errorf("Expected third issue to be P2, got P%d", ready[2].Priority)
	}
}

func TestGetReadyWorkWithPriorityFilter(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Create issues with different priorities
	issueP0 := &types.Issue{Title: "P0", Status: types.StatusOpen, Priority: 0, IssueType: types.TypeTask}
	issueP1 := &types.Issue{Title: "P1", Status: types.StatusOpen, Priority: 1, IssueType: types.TypeTask}
	issueP2 := &types.Issue{Title: "P2", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask}

	store.CreateIssue(ctx, issueP0, "test-user")
	store.CreateIssue(ctx, issueP1, "test-user")
	store.CreateIssue(ctx, issueP2, "test-user")

	// Filter for P0 only
	priority0 := 0
	ready, err := store.GetReadyWork(ctx, types.WorkFilter{Status: types.StatusOpen, Priority: &priority0})
	if err != nil {
		t.Fatalf("GetReadyWork failed: %v", err)
	}

	if len(ready) != 1 {
		t.Fatalf("Expected 1 P0 issue, got %d", len(ready))
	}

	if ready[0].Priority != 0 {
		t.Errorf("Expected P0 issue, got P%d", ready[0].Priority)
	}
}

func TestGetReadyWorkWithAssigneeFilter(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Create issues with different assignees
	issueAlice := &types.Issue{Title: "Alice's task", Status: types.StatusOpen, Priority: 1, IssueType: types.TypeTask, Assignee: "alice"}
	issueBob := &types.Issue{Title: "Bob's task", Status: types.StatusOpen, Priority: 1, IssueType: types.TypeTask, Assignee: "bob"}
	issueUnassigned := &types.Issue{Title: "Unassigned", Status: types.StatusOpen, Priority: 1, IssueType: types.TypeTask}

	store.CreateIssue(ctx, issueAlice, "test-user")
	store.CreateIssue(ctx, issueBob, "test-user")
	store.CreateIssue(ctx, issueUnassigned, "test-user")

	// Filter for alice
	assignee := "alice"
	ready, err := store.GetReadyWork(ctx, types.WorkFilter{Status: types.StatusOpen, Assignee: &assignee})
	if err != nil {
		t.Fatalf("GetReadyWork failed: %v", err)
	}

	if len(ready) != 1 {
		t.Fatalf("Expected 1 issue for alice, got %d", len(ready))
	}

	if ready[0].Assignee != "alice" {
		t.Errorf("Expected alice's issue, got %s", ready[0].Assignee)
	}
}

func TestGetReadyWorkWithLimit(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Create 5 ready issues
	for i := 0; i < 5; i++ {
		issue := &types.Issue{Title: "Task", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask}
		store.CreateIssue(ctx, issue, "test-user")
	}

	// Limit to 3
	ready, err := store.GetReadyWork(ctx, types.WorkFilter{Status: types.StatusOpen, Limit: 3})
	if err != nil {
		t.Fatalf("GetReadyWork failed: %v", err)
	}

	if len(ready) != 3 {
		t.Errorf("Expected 3 issues (limit), got %d", len(ready))
	}
}

func TestGetReadyWorkIgnoresRelatedDeps(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Create two issues with "related" dependency (should not block)
	issue1 := &types.Issue{Title: "First", Status: types.StatusOpen, Priority: 1, IssueType: types.TypeTask}
	issue2 := &types.Issue{Title: "Second", Status: types.StatusOpen, Priority: 1, IssueType: types.TypeTask}

	store.CreateIssue(ctx, issue1, "test-user")
	store.CreateIssue(ctx, issue2, "test-user")

	// Add "related" dependency (not blocking)
	store.AddDependency(ctx, &types.Dependency{IssueID: issue2.ID, DependsOnID: issue1.ID, Type: types.DepRelated}, "test-user")

	// Both should be ready (related deps don't block)
	ready, err := store.GetReadyWork(ctx, types.WorkFilter{Status: types.StatusOpen})
	if err != nil {
		t.Fatalf("GetReadyWork failed: %v", err)
	}

	if len(ready) != 2 {
		t.Fatalf("Expected 2 ready issues (related deps don't block), got %d", len(ready))
	}
}

func TestGetBlockedIssues(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Create issues:
	// bd-1: open, no dependencies → not blocked
	// bd-2: open, depends on bd-1 (open) → blocked by bd-1
	// bd-3: open, depends on bd-1 and bd-2 (both open) → blocked by 2 issues

	issue1 := &types.Issue{Title: "Foundation", Status: types.StatusOpen, Priority: 1, IssueType: types.TypeTask}
	issue2 := &types.Issue{Title: "Blocked by 1", Status: types.StatusOpen, Priority: 1, IssueType: types.TypeTask}
	issue3 := &types.Issue{Title: "Blocked by 2", Status: types.StatusOpen, Priority: 1, IssueType: types.TypeTask}

	store.CreateIssue(ctx, issue1, "test-user")
	store.CreateIssue(ctx, issue2, "test-user")
	store.CreateIssue(ctx, issue3, "test-user")

	store.AddDependency(ctx, &types.Dependency{IssueID: issue2.ID, DependsOnID: issue1.ID, Type: types.DepBlocks}, "test-user")
	store.AddDependency(ctx, &types.Dependency{IssueID: issue3.ID, DependsOnID: issue1.ID, Type: types.DepBlocks}, "test-user")
	store.AddDependency(ctx, &types.Dependency{IssueID: issue3.ID, DependsOnID: issue2.ID, Type: types.DepBlocks}, "test-user")

	// Get blocked issues
	blocked, err := store.GetBlockedIssues(ctx)
	if err != nil {
		t.Fatalf("GetBlockedIssues failed: %v", err)
	}

	if len(blocked) != 2 {
		t.Fatalf("Expected 2 blocked issues, got %d", len(blocked))
	}

	// Find issue3 in blocked list
	var issue3Blocked *types.BlockedIssue
	for i := range blocked {
		if blocked[i].ID == issue3.ID {
			issue3Blocked = blocked[i]
			break
		}
	}

	if issue3Blocked == nil {
		t.Fatal("Expected issue3 to be in blocked list")
	}

	if issue3Blocked.BlockedByCount != 2 {
		t.Errorf("Expected issue3 to be blocked by 2 issues, got %d", issue3Blocked.BlockedByCount)
	}

	// Verify the blockers are correct
	if len(issue3Blocked.BlockedBy) != 2 {
		t.Errorf("Expected 2 blocker IDs, got %d", len(issue3Blocked.BlockedBy))
	}
}
