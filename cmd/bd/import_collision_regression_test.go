package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/steveyegge/beads/internal/storage/sqlite"
	"github.com/steveyegge/beads/internal/types"
)

const (
	testIssueBD1 = "bd-1"
	testIssueBD2 = "bd-2"
)

// TestRemapCollisionsRemapsImportedNotExisting verifies the bug fix where collision
// resolution incorrectly modified existing issue dependencies.
//
// Bug (fixed): updateDependencyReferences() was updating ALL dependencies in the database
// based on the idMapping, without distinguishing between dependencies belonging to
// IMPORTED issues (should be updated) vs EXISTING issues (should NOT be touched).
//
// This test ensures existing issue dependencies are preserved during collision resolution.
func TestRemapCollisionsRemapsImportedNotExisting(t *testing.T) {
	// Setup: Create temporary database
	tmpDir, err := os.MkdirTemp("", "collision-bug-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.db")
	store := newTestStoreWithPrefix(t, dbPath, "bd")

	ctx := context.Background()

	// Step 1: Create existing issues with dependencies
	existingIssues := []*types.Issue{
		{
			ID:          testIssueBD1,
			Title:       "Existing BD-1",
			Description: "Original database issue 1, depends on bd-2",
			Status:      types.StatusOpen,
			Priority:    2,
			IssueType:   types.TypeTask,
		},
		{
			ID:          testIssueBD2,
			Title:       "Existing BD-2",
			Description: "Original database issue 2",
			Status:      types.StatusOpen,
			Priority:    2,
			IssueType:   types.TypeTask,
		},
		{
			ID:          "bd-3",
			Title:       "Existing BD-3",
			Description: "Original database issue 3, depends on " + testIssueBD1,
			Status:      types.StatusOpen,
			Priority:    2,
			IssueType:   types.TypeTask,
		},
	}

	for _, issue := range existingIssues {
		if err := store.CreateIssue(ctx, issue, "test"); err != nil {
			t.Fatalf("failed to create existing issue %s: %v", issue.ID, err)
		}
	}

	// Add dependencies between existing issues
	dep1 := &types.Dependency{
		IssueID:     testIssueBD1,
		DependsOnID: testIssueBD2,
		Type:        types.DepBlocks,
	}
	dep2 := &types.Dependency{
		IssueID:     "bd-3",
		DependsOnID: testIssueBD1,
		Type:        types.DepBlocks,
	}
	if err := store.AddDependency(ctx, dep1, "test"); err != nil {
		t.Fatalf("failed to add dependency bd-1 → bd-2: %v", err)
	}
	if err := store.AddDependency(ctx, dep2, "test"); err != nil {
		t.Fatalf("failed to add dependency bd-3 → bd-1: %v", err)
	}

	// Step 2: Simulate importing issues with same IDs but different content
	importedIssues := []*types.Issue{
		{
			ID:          testIssueBD1,
			Title:       "Imported BD-1",
			Description: "From import",
			Status:      types.StatusOpen,
			Priority:    1,
			IssueType:   types.TypeTask,
		},
		{
			ID:          testIssueBD2,
			Title:       "Imported BD-2",
			Description: "From import",
			Status:      types.StatusOpen,
			Priority:    1,
			IssueType:   types.TypeTask,
		},
		{
			ID:          "bd-3",
			Title:       "Imported BD-3",
			Description: "From import",
			Status:      types.StatusOpen,
			Priority:    1,
			IssueType:   types.TypeTask,
		},
	}

	// Step 3: Detect collisions
	collisionResult, err := sqlite.DetectCollisions(ctx, store, importedIssues)
	if err != nil {
		t.Fatalf("collision detection failed: %v", err)
	}

	if len(collisionResult.Collisions) != 3 {
		t.Fatalf("expected 3 collisions, got %d", len(collisionResult.Collisions))
	}

	// Step 4: Resolve collisions
	allExisting, err := store.SearchIssues(ctx, "", types.IssueFilter{})
	if err != nil {
		t.Fatalf("failed to get existing issues: %v", err)
	}

	if err := sqlite.ScoreCollisions(ctx, store, collisionResult.Collisions, allExisting); err != nil {
		t.Fatalf("failed to score collisions: %v", err)
	}

	idMapping, err := sqlite.RemapCollisions(ctx, store, collisionResult.Collisions, allExisting)
	if err != nil {
		t.Fatalf("RemapCollisions failed: %v", err)
	}

	// Step 5: Verify dependencies are preserved on remapped issues
	// With content-hash scoring, all existing issues get remapped to new IDs
	t.Logf("\n=== Verifying Dependencies Preserved on Remapped Issues ===")
	t.Logf("ID Mappings: %v", idMapping)

	// The new bd-1, bd-2, bd-3 (incoming issues) should have NO dependencies
	newBD1Deps, _ := store.GetDependencyRecords(ctx, "bd-1")
	if len(newBD1Deps) != 0 {
		t.Errorf("Expected 0 dependencies for new bd-1 (incoming), got %d", len(newBD1Deps))
	}

	newBD3Deps, _ := store.GetDependencyRecords(ctx, "bd-3")
	if len(newBD3Deps) != 0 {
		t.Errorf("Expected 0 dependencies for new bd-3 (incoming), got %d", len(newBD3Deps))
	}

	// The remapped issues should have their dependencies preserved
	remappedBD1 := idMapping["bd-1"]  // Old bd-1 → new ID
	remappedBD2 := idMapping["bd-2"]  // Old bd-2 → new ID
	remappedBD3 := idMapping["bd-3"]  // Old bd-3 → new ID

	// Check remapped bd-1's dependency (was bd-1 → bd-2, now should be remappedBD1 → remappedBD2)
	remappedBD1Deps, _ := store.GetDependencyRecords(ctx, remappedBD1)
	t.Logf("%s dependencies: %d (expected: 1)", remappedBD1, len(remappedBD1Deps))
	
	if len(remappedBD1Deps) != 1 {
		t.Errorf("Expected 1 dependency for remapped %s (preserved from old bd-1), got %d",
			remappedBD1, len(remappedBD1Deps))
	} else if remappedBD1Deps[0].DependsOnID != remappedBD2 {
		t.Errorf("Expected %s → %s, got %s → %s", 
			remappedBD1, remappedBD2, remappedBD1, remappedBD1Deps[0].DependsOnID)
	}

	// Check remapped bd-3's dependency (was bd-3 → bd-1, now should be remappedBD3 → remappedBD1)
	remappedBD3Deps, _ := store.GetDependencyRecords(ctx, remappedBD3)
	t.Logf("%s dependencies: %d (expected: 1)", remappedBD3, len(remappedBD3Deps))
	
	if len(remappedBD3Deps) != 1 {
		t.Errorf("Expected 1 dependency for remapped %s (preserved from old bd-3), got %d",
			remappedBD3, len(remappedBD3Deps))
	} else if remappedBD3Deps[0].DependsOnID != remappedBD1 {
		t.Errorf("Expected %s → %s, got %s → %s", 
			remappedBD3, remappedBD1, remappedBD3, remappedBD3Deps[0].DependsOnID)
	}

	t.Logf("Fix verified: Dependencies preserved correctly on remapped issues with content-hash scoring")
}

// TestRemapCollisionsDoesNotUpdateNonexistentDependencies verifies that
// updateDependencyReferences is effectively a no-op during normal import flow,
// since imported dependencies haven't been added to the database yet when
// RemapCollisions runs.
//
// This test demonstrates that even if we had dependencies with the old imported IDs
// in the database, they are NOT touched because they don't have the NEW remapped IDs.
func TestRemapCollisionsDoesNotUpdateNonexistentDependencies(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "collision-noop-deps-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.db")
	store := newTestStoreWithPrefix(t, dbPath, "bd")

	ctx := context.Background()

	// Step 1: Create existing issue with dependency
	existing1 := &types.Issue{
		ID:          testIssueBD1,
		Title:       "Existing BD-1",
		Description: "Original database issue",
		Status:      types.StatusOpen,
		Priority:    2,
		IssueType:   types.TypeTask,
	}
	existing2 := &types.Issue{
		ID:          testIssueBD2,
		Title:       "Existing BD-2",
		Description: "Original database issue",
		Status:      types.StatusOpen,
		Priority:    2,
		IssueType:   types.TypeTask,
	}
	if err := store.CreateIssue(ctx, existing1, "test"); err != nil {
		t.Fatalf("failed to create existing issue bd-1: %v", err)
	}
	if err := store.CreateIssue(ctx, existing2, "test"); err != nil {
		t.Fatalf("failed to create existing issue bd-2: %v", err)
	}

	// Add dependency between existing issues
	existingDep := &types.Dependency{
		IssueID:     testIssueBD1,
		DependsOnID: testIssueBD2,
		Type:        types.DepBlocks,
	}
	if err := store.AddDependency(ctx, existingDep, "test"); err != nil {
		t.Fatalf("failed to add existing dependency: %v", err)
	}

	// Step 2: Import colliding issues (without dependencies in DB)
	imported := []*types.Issue{
		{
			ID:          testIssueBD1,
			Title:       "Imported BD-1",
			Description: "From import, will be remapped",
			Status:      types.StatusOpen,
			Priority:    1,
			IssueType:   types.TypeTask,
		},
	}

	// Detect and resolve collisions
	collisionResult, err := sqlite.DetectCollisions(ctx, store, imported)
	if err != nil {
		t.Fatalf("collision detection failed: %v", err)
	}

	allExisting, _ := store.SearchIssues(ctx, "", types.IssueFilter{})
	if err := sqlite.ScoreCollisions(ctx, store, collisionResult.Collisions, allExisting); err != nil {
		t.Fatalf("failed to score collisions: %v", err)
	}

	// Now remap collisions - this should NOT touch the existing bd-1 → bd-2 dependency
	idMapping, err := sqlite.RemapCollisions(ctx, store, collisionResult.Collisions, allExisting)
	if err != nil {
		t.Fatalf("RemapCollisions failed: %v", err)
	}

	// Step 3: Verify dependencies are preserved correctly
	// With content-hash scoring: existing hash > incoming hash, so RemapIncoming=false
	// This means: existing bd-1 → remapped to new ID, incoming bd-1 takes over bd-1
	
	// The remapped issue (old bd-1) should have its dependency preserved
	remappedID := idMapping["bd-1"]
	remappedDeps, err := store.GetDependencyRecords(ctx, remappedID)
	if err != nil {
		t.Fatalf("failed to get dependencies for %s: %v", remappedID, err)
	}

	if len(remappedDeps) != 1 {
		t.Errorf("Expected 1 dependency for remapped %s (preserved from old bd-1), got %d",
			remappedID, len(remappedDeps))
	} else {
		// The dependency should now be remappedID → bd-2 (updated from bd-1 → bd-2)
		if remappedDeps[0].DependsOnID != testIssueBD2 {
			t.Errorf("Expected %s → bd-2, got %s → %s", remappedID, remappedID, remappedDeps[0].DependsOnID)
		}
	}

	// The new bd-1 (incoming issue) should have no dependencies
	// (because dependencies are imported later in Phase 5)
	newBD1Deps, err := store.GetDependencyRecords(ctx, "bd-1")
	if err != nil {
		t.Fatalf("failed to get dependencies for bd-1: %v", err)
	}

	if len(newBD1Deps) != 0 {
		t.Errorf("Expected 0 dependencies for new bd-1 (dependencies added later), got %d", len(newBD1Deps))
	}

	t.Logf("Verified: Dependencies preserved correctly during collision resolution with content-hash scoring")
}
