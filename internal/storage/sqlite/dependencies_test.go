package sqlite

import (
	"context"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// Helper function to test adding a dependency with a specific type
func testAddDependencyWithType(t *testing.T, depType types.DependencyType, title1, title2 string) {
	t.Helper()

	store, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Create two issues
	issue1 := &types.Issue{Title: title1, Status: types.StatusOpen, Priority: 1, IssueType: types.TypeTask}
	issue2 := &types.Issue{Title: title2, Status: types.StatusOpen, Priority: 1, IssueType: types.TypeTask}

	store.CreateIssue(ctx, issue1, "test-user")
	store.CreateIssue(ctx, issue2, "test-user")

	// Add dependency (issue2 depends on issue1)
	dep := &types.Dependency{
		IssueID:     issue2.ID,
		DependsOnID: issue1.ID,
		Type:        depType,
	}

	err := store.AddDependency(ctx, dep, "test-user")
	if err != nil {
		t.Fatalf("AddDependency failed: %v", err)
	}

	// Verify dependency was added
	deps, err := store.GetDependencies(ctx, issue2.ID)
	if err != nil {
		t.Fatalf("GetDependencies failed: %v", err)
	}

	if len(deps) != 1 {
		t.Fatalf("Expected 1 dependency, got %d", len(deps))
	}

	if deps[0].ID != issue1.ID {
		t.Errorf("Expected dependency on %s, got %s", issue1.ID, deps[0].ID)
	}
}

func TestAddDependency(t *testing.T) {
	testAddDependencyWithType(t, types.DepBlocks, "First", "Second")
}

func TestAddDependencyDiscoveredFrom(t *testing.T) {
	testAddDependencyWithType(t, types.DepDiscoveredFrom, "Parent task", "Bug found during work")
}

func TestRemoveDependency(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Create and link issues
	issue1 := &types.Issue{Title: "First", Status: types.StatusOpen, Priority: 1, IssueType: types.TypeTask}
	issue2 := &types.Issue{Title: "Second", Status: types.StatusOpen, Priority: 1, IssueType: types.TypeTask}

	store.CreateIssue(ctx, issue1, "test-user")
	store.CreateIssue(ctx, issue2, "test-user")

	dep := &types.Dependency{
		IssueID:     issue2.ID,
		DependsOnID: issue1.ID,
		Type:        types.DepBlocks,
	}
	store.AddDependency(ctx, dep, "test-user")

	// Remove the dependency
	err := store.RemoveDependency(ctx, issue2.ID, issue1.ID, "test-user")
	if err != nil {
		t.Fatalf("RemoveDependency failed: %v", err)
	}

	// Verify dependency was removed
	deps, err := store.GetDependencies(ctx, issue2.ID)
	if err != nil {
		t.Fatalf("GetDependencies failed: %v", err)
	}

	if len(deps) != 0 {
		t.Errorf("Expected 0 dependencies after removal, got %d", len(deps))
	}
}

func TestGetDependents(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Create issues: bd-2 and bd-3 both depend on bd-1
	issue1 := &types.Issue{Title: "Foundation", Status: types.StatusOpen, Priority: 1, IssueType: types.TypeTask}
	issue2 := &types.Issue{Title: "Feature A", Status: types.StatusOpen, Priority: 1, IssueType: types.TypeTask}
	issue3 := &types.Issue{Title: "Feature B", Status: types.StatusOpen, Priority: 1, IssueType: types.TypeTask}

	store.CreateIssue(ctx, issue1, "test-user")
	store.CreateIssue(ctx, issue2, "test-user")
	store.CreateIssue(ctx, issue3, "test-user")

	store.AddDependency(ctx, &types.Dependency{IssueID: issue2.ID, DependsOnID: issue1.ID, Type: types.DepBlocks}, "test-user")
	store.AddDependency(ctx, &types.Dependency{IssueID: issue3.ID, DependsOnID: issue1.ID, Type: types.DepBlocks}, "test-user")

	// Get dependents of issue1
	dependents, err := store.GetDependents(ctx, issue1.ID)
	if err != nil {
		t.Fatalf("GetDependents failed: %v", err)
	}

	if len(dependents) != 2 {
		t.Fatalf("Expected 2 dependents, got %d", len(dependents))
	}

	// Verify both dependents are present
	foundIDs := make(map[string]bool)
	for _, dep := range dependents {
		foundIDs[dep.ID] = true
	}

	if !foundIDs[issue2.ID] || !foundIDs[issue3.ID] {
		t.Errorf("Expected dependents %s and %s", issue2.ID, issue3.ID)
	}
}

func TestGetDependencyTree(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Create a chain: bd-3 → bd-2 → bd-1
	issue1 := &types.Issue{Title: "Level 0", Status: types.StatusOpen, Priority: 1, IssueType: types.TypeTask}
	issue2 := &types.Issue{Title: "Level 1", Status: types.StatusOpen, Priority: 1, IssueType: types.TypeTask}
	issue3 := &types.Issue{Title: "Level 2", Status: types.StatusOpen, Priority: 1, IssueType: types.TypeTask}

	store.CreateIssue(ctx, issue1, "test-user")
	store.CreateIssue(ctx, issue2, "test-user")
	store.CreateIssue(ctx, issue3, "test-user")

	store.AddDependency(ctx, &types.Dependency{IssueID: issue2.ID, DependsOnID: issue1.ID, Type: types.DepBlocks}, "test-user")
	store.AddDependency(ctx, &types.Dependency{IssueID: issue3.ID, DependsOnID: issue2.ID, Type: types.DepBlocks}, "test-user")

	// Get tree starting from issue3
	tree, err := store.GetDependencyTree(ctx, issue3.ID, 10)
	if err != nil {
		t.Fatalf("GetDependencyTree failed: %v", err)
	}

	if len(tree) != 3 {
		t.Fatalf("Expected 3 nodes in tree, got %d", len(tree))
	}

	// Verify depths
	depthMap := make(map[string]int)
	for _, node := range tree {
		depthMap[node.ID] = node.Depth
	}

	if depthMap[issue3.ID] != 0 {
		t.Errorf("Expected depth 0 for %s, got %d", issue3.ID, depthMap[issue3.ID])
	}

	if depthMap[issue2.ID] != 1 {
		t.Errorf("Expected depth 1 for %s, got %d", issue2.ID, depthMap[issue2.ID])
	}

	if depthMap[issue1.ID] != 2 {
		t.Errorf("Expected depth 2 for %s, got %d", issue1.ID, depthMap[issue1.ID])
	}
}

func TestDetectCycles(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Try to create a cycle: bd-1 → bd-2 → bd-3 → bd-1
	// This should be prevented by AddDependency
	issue1 := &types.Issue{Title: "First", Status: types.StatusOpen, Priority: 1, IssueType: types.TypeTask}
	issue2 := &types.Issue{Title: "Second", Status: types.StatusOpen, Priority: 1, IssueType: types.TypeTask}
	issue3 := &types.Issue{Title: "Third", Status: types.StatusOpen, Priority: 1, IssueType: types.TypeTask}

	store.CreateIssue(ctx, issue1, "test-user")
	store.CreateIssue(ctx, issue2, "test-user")
	store.CreateIssue(ctx, issue3, "test-user")

	// Add first two dependencies successfully
	err := store.AddDependency(ctx, &types.Dependency{IssueID: issue1.ID, DependsOnID: issue2.ID, Type: types.DepBlocks}, "test-user")
	if err != nil {
		t.Fatalf("First dependency failed: %v", err)
	}

	err = store.AddDependency(ctx, &types.Dependency{IssueID: issue2.ID, DependsOnID: issue3.ID, Type: types.DepBlocks}, "test-user")
	if err != nil {
		t.Fatalf("Second dependency failed: %v", err)
	}

	// The third dependency should fail because it would create a cycle
	err = store.AddDependency(ctx, &types.Dependency{IssueID: issue3.ID, DependsOnID: issue1.ID, Type: types.DepBlocks}, "test-user")
	if err == nil {
		t.Fatal("Expected error when creating cycle, but got none")
	}

	// Verify no cycles exist
	cycles, err := store.DetectCycles(ctx)
	if err != nil {
		t.Fatalf("DetectCycles failed: %v", err)
	}

	if len(cycles) != 0 {
		t.Errorf("Expected no cycles after prevention, but found %d", len(cycles))
	}
}

func TestNoCyclesDetected(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Create a valid chain with no cycles
	issue1 := &types.Issue{Title: "First", Status: types.StatusOpen, Priority: 1, IssueType: types.TypeTask}
	issue2 := &types.Issue{Title: "Second", Status: types.StatusOpen, Priority: 1, IssueType: types.TypeTask}

	store.CreateIssue(ctx, issue1, "test-user")
	store.CreateIssue(ctx, issue2, "test-user")

	store.AddDependency(ctx, &types.Dependency{IssueID: issue2.ID, DependsOnID: issue1.ID, Type: types.DepBlocks}, "test-user")

	cycles, err := store.DetectCycles(ctx)
	if err != nil {
		t.Fatalf("DetectCycles failed: %v", err)
	}

	if len(cycles) != 0 {
		t.Errorf("Expected no cycles, but found %d", len(cycles))
	}
}
