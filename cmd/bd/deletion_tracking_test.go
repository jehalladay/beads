package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/steveyegge/beads/internal/storage/sqlite"
	"github.com/steveyegge/beads/internal/types"
)

// TestMultiWorkspaceDeletionSync simulates the bd-hv01 bug scenario:
// Clone A deletes an issue, Clone B still has it, and after sync it should stay deleted
func TestMultiWorkspaceDeletionSync(t *testing.T) {
	// Setup two separate workspaces simulating two git clones
	cloneADir := t.TempDir()
	cloneBDir := t.TempDir()

	cloneAJSONL := filepath.Join(cloneADir, "beads.jsonl")
	cloneBJSONL := filepath.Join(cloneBDir, "beads.jsonl")

	cloneADB := filepath.Join(cloneADir, "beads.db")
	cloneBDB := filepath.Join(cloneBDir, "beads.db")

	ctx := context.Background()

	// Create stores for both clones
	storeA, err := sqlite.New(cloneADB)
	if err != nil {
		t.Fatalf("Failed to create store A: %v", err)
	}
	defer storeA.Close()

	if err := storeA.SetConfig(ctx, "issue_prefix", "bd"); err != nil {
		t.Fatalf("Failed to set issue_prefix for store A: %v", err)
	}

	storeB, err := sqlite.New(cloneBDB)
	if err != nil {
		t.Fatalf("Failed to create store B: %v", err)
	}
	defer storeB.Close()

	if err := storeB.SetConfig(ctx, "issue_prefix", "bd"); err != nil {
		t.Fatalf("Failed to set issue_prefix for store B: %v", err)
	}

	// Step 1: Both clones start with the same two issues
	issueToDelete := &types.Issue{
		ID:          "bd-delete-me",
		Title:       "Issue to be deleted",
		Description: "This will be deleted in clone A",
		Status:      types.StatusOpen,
		Priority:    1,
		IssueType:   "bug",
	}

	issueToKeep := &types.Issue{
		ID:          "bd-keep-me",
		Title:       "Issue to keep",
		Description: "This should remain",
		Status:      types.StatusOpen,
		Priority:    1,
		IssueType:   "feature",
	}

	// Create in both stores (using "test" as actor)
	if err := storeA.CreateIssue(ctx, issueToDelete, "test"); err != nil {
		t.Fatalf("Failed to create issue in store A: %v", err)
	}
	if err := storeA.CreateIssue(ctx, issueToKeep, "test"); err != nil {
		t.Fatalf("Failed to create issue in store A: %v", err)
	}

	if err := storeB.CreateIssue(ctx, issueToDelete, "test"); err != nil {
		t.Fatalf("Failed to create issue in store B: %v", err)
	}
	if err := storeB.CreateIssue(ctx, issueToKeep, "test"); err != nil {
		t.Fatalf("Failed to create issue in store B: %v", err)
	}

	// Export from both
	if err := exportToJSONLWithStore(ctx, storeA, cloneAJSONL); err != nil {
		t.Fatalf("Failed to export from store A: %v", err)
	}
	if err := exportToJSONLWithStore(ctx, storeB, cloneBJSONL); err != nil {
		t.Fatalf("Failed to export from store B: %v", err)
	}

	// Initialize base snapshots for both (simulating first sync)
	if err := initializeSnapshotsIfNeeded(cloneAJSONL); err != nil {
		t.Fatalf("Failed to initialize snapshots for A: %v", err)
	}
	if err := initializeSnapshotsIfNeeded(cloneBJSONL); err != nil {
		t.Fatalf("Failed to initialize snapshots for B: %v", err)
	}

	// Step 2: Clone A deletes the issue
	if err := storeA.DeleteIssue(ctx, "bd-delete-me"); err != nil {
		t.Fatalf("Failed to delete issue in store A: %v", err)
	}

	// Step 3: Clone A exports and captures left snapshot (simulating pre-pull)
	if err := exportToJSONLWithStore(ctx, storeA, cloneAJSONL); err != nil {
		t.Fatalf("Failed to export from store A after deletion: %v", err)
	}
	if err := captureLeftSnapshot(cloneAJSONL); err != nil {
		t.Fatalf("Failed to capture left snapshot for A: %v", err)
	}

	// Simulate git push/pull: Copy Clone A's JSONL to Clone B's "remote" state
	remoteJSONL := cloneAJSONL

	// Step 4: Clone B exports (still has both issues) and captures left snapshot
	if err := exportToJSONLWithStore(ctx, storeB, cloneBJSONL); err != nil {
		t.Fatalf("Failed to export from store B: %v", err)
	}
	if err := captureLeftSnapshot(cloneBJSONL); err != nil {
		t.Fatalf("Failed to capture left snapshot for B: %v", err)
	}

	// Step 5: Simulate Clone B pulling from remote (copy remote JSONL)
	remoteData, err := os.ReadFile(remoteJSONL)
	if err != nil {
		t.Fatalf("Failed to read remote JSONL: %v", err)
	}
	if err := os.WriteFile(cloneBJSONL, remoteData, 0644); err != nil {
		t.Fatalf("Failed to write pulled JSONL to clone B: %v", err)
	}

	// Step 6: Clone B applies 3-way merge and prunes deletions
	// This is the key fix - it should detect that bd-delete-me was deleted remotely
	merged, err := merge3WayAndPruneDeletions(ctx, storeB, cloneBJSONL)
	if err != nil {
		t.Fatalf("Failed to apply deletions from merge: %v", err)
	}

	if !merged {
		t.Error("Expected 3-way merge to run, but it was skipped")
	}

	// Step 7: Verify the deletion was applied to Clone B's database
	deletedIssue, err := storeB.GetIssue(ctx, "bd-delete-me")
	if err == nil && deletedIssue != nil {
		t.Errorf("Issue bd-delete-me should have been deleted from Clone B, but still exists")
	}

	// Verify the kept issue still exists
	keptIssue, err := storeB.GetIssue(ctx, "bd-keep-me")
	if err != nil || keptIssue == nil {
		t.Errorf("Issue bd-keep-me should still exist in Clone B")
	}

	// Verify Clone A still has only one issue
	issuesA, err := storeA.SearchIssues(ctx, "", types.IssueFilter{})
	if err != nil {
		t.Fatalf("Failed to search issues in store A: %v", err)
	}
	if len(issuesA) != 1 {
		t.Errorf("Clone A should have 1 issue after deletion, got %d", len(issuesA))
	}

	// Verify Clone B now matches Clone A (both have 1 issue)
	issuesB, err := storeB.SearchIssues(ctx, "", types.IssueFilter{})
	if err != nil {
		t.Fatalf("Failed to search issues in store B: %v", err)
	}
	if len(issuesB) != 1 {
		t.Errorf("Clone B should have 1 issue after merge, got %d", len(issuesB))
	}
}

// TestDeletionWithLocalModification tests the conflict scenario:
// Remote deletes an issue, but local has modified it
func TestDeletionWithLocalModification(t *testing.T) {
	dir := t.TempDir()
	jsonlPath := filepath.Join(dir, "beads.jsonl")
	dbPath := filepath.Join(dir, "beads.db")

	ctx := context.Background()

	store, err := sqlite.New(dbPath)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer store.Close()

	if err := store.SetConfig(ctx, "issue_prefix", "bd"); err != nil {
		t.Fatalf("Failed to set issue_prefix: %v", err)
	}

	// Create an issue
	issue := &types.Issue{
		ID:          "bd-conflict",
		Title:       "Original title",
		Description: "Original description",
		Status:      types.StatusOpen,
		Priority:    1,
		IssueType:   "bug",
	}

	if err := store.CreateIssue(ctx, issue, "test"); err != nil {
		t.Fatalf("Failed to create issue: %v", err)
	}

	// Export and create base snapshot
	if err := exportToJSONLWithStore(ctx, store, jsonlPath); err != nil {
		t.Fatalf("Failed to export: %v", err)
	}
	if err := initializeSnapshotsIfNeeded(jsonlPath); err != nil {
		t.Fatalf("Failed to initialize snapshots: %v", err)
	}

	// Modify the issue locally
	updates := map[string]interface{}{
		"title": "Modified title locally",
	}
	if err := store.UpdateIssue(ctx, "bd-conflict", updates, "test"); err != nil {
		t.Fatalf("Failed to update issue: %v", err)
	}

	// Export modified state and capture left snapshot
	if err := exportToJSONLWithStore(ctx, store, jsonlPath); err != nil {
		t.Fatalf("Failed to export after modification: %v", err)
	}
	if err := captureLeftSnapshot(jsonlPath); err != nil {
		t.Fatalf("Failed to capture left snapshot: %v", err)
	}

	// Simulate remote deletion (write empty JSONL)
	if err := os.WriteFile(jsonlPath, []byte(""), 0644); err != nil {
		t.Fatalf("Failed to simulate remote deletion: %v", err)
	}

	// Try to merge - this should detect a conflict (modified locally, deleted remotely)
	_, err = merge3WayAndPruneDeletions(ctx, store, jsonlPath)
	if err == nil {
		t.Error("Expected merge conflict error, but got nil")
	}

	// The issue should still exist in the database (conflict not auto-resolved)
	conflictIssue, err := store.GetIssue(ctx, "bd-conflict")
	if err != nil || conflictIssue == nil {
		t.Error("Issue should still exist after conflict")
	}
}

// TestComputeAcceptedDeletions tests the deletion detection logic
func TestComputeAcceptedDeletions(t *testing.T) {
	dir := t.TempDir()

	basePath := filepath.Join(dir, "base.jsonl")
	leftPath := filepath.Join(dir, "left.jsonl")
	mergedPath := filepath.Join(dir, "merged.jsonl")

	// Base has 3 issues
	baseContent := `{"id":"bd-1","title":"Issue 1"}
{"id":"bd-2","title":"Issue 2"}
{"id":"bd-3","title":"Issue 3"}
`

	// Left has 3 issues (unchanged from base)
	leftContent := baseContent

	// Merged has only 2 issues (bd-2 was deleted remotely)
	mergedContent := `{"id":"bd-1","title":"Issue 1"}
{"id":"bd-3","title":"Issue 3"}
`

	if err := os.WriteFile(basePath, []byte(baseContent), 0644); err != nil {
		t.Fatalf("Failed to write base: %v", err)
	}
	if err := os.WriteFile(leftPath, []byte(leftContent), 0644); err != nil {
		t.Fatalf("Failed to write left: %v", err)
	}
	if err := os.WriteFile(mergedPath, []byte(mergedContent), 0644); err != nil {
		t.Fatalf("Failed to write merged: %v", err)
	}

	deletions, err := computeAcceptedDeletions(basePath, leftPath, mergedPath)
	if err != nil {
		t.Fatalf("Failed to compute deletions: %v", err)
	}

	if len(deletions) != 1 {
		t.Errorf("Expected 1 deletion, got %d", len(deletions))
	}

	if len(deletions) > 0 && deletions[0] != "bd-2" {
		t.Errorf("Expected deletion of bd-2, got %s", deletions[0])
	}
}

// TestComputeAcceptedDeletions_LocallyModified tests that locally modified issues are not deleted
func TestComputeAcceptedDeletions_LocallyModified(t *testing.T) {
	dir := t.TempDir()

	basePath := filepath.Join(dir, "base.jsonl")
	leftPath := filepath.Join(dir, "left.jsonl")
	mergedPath := filepath.Join(dir, "merged.jsonl")

	// Base has 2 issues
	baseContent := `{"id":"bd-1","title":"Original 1"}
{"id":"bd-2","title":"Original 2"}
`

	// Left has bd-2 modified locally
	leftContent := `{"id":"bd-1","title":"Original 1"}
{"id":"bd-2","title":"Modified locally"}
`

	// Merged has only bd-1 (bd-2 deleted remotely, but we modified it locally)
	mergedContent := `{"id":"bd-1","title":"Original 1"}
`

	if err := os.WriteFile(basePath, []byte(baseContent), 0644); err != nil {
		t.Fatalf("Failed to write base: %v", err)
	}
	if err := os.WriteFile(leftPath, []byte(leftContent), 0644); err != nil {
		t.Fatalf("Failed to write left: %v", err)
	}
	if err := os.WriteFile(mergedPath, []byte(mergedContent), 0644); err != nil {
		t.Fatalf("Failed to write merged: %v", err)
	}

	deletions, err := computeAcceptedDeletions(basePath, leftPath, mergedPath)
	if err != nil {
		t.Fatalf("Failed to compute deletions: %v", err)
	}

	// bd-2 should NOT be in accepted deletions because it was modified locally
	if len(deletions) != 0 {
		t.Errorf("Expected 0 deletions (locally modified), got %d: %v", len(deletions), deletions)
	}
}

// TestSnapshotManagement tests the snapshot file lifecycle
func TestSnapshotManagement(t *testing.T) {
	dir := t.TempDir()
	jsonlPath := filepath.Join(dir, "beads.jsonl")

	// Write initial JSONL
	content := `{"id":"bd-1","title":"Test"}
`
	if err := os.WriteFile(jsonlPath, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write JSONL: %v", err)
	}

	// Initialize snapshots
	if err := initializeSnapshotsIfNeeded(jsonlPath); err != nil {
		t.Fatalf("Failed to initialize snapshots: %v", err)
	}

	basePath, leftPath := getSnapshotPaths(jsonlPath)

	// Base should exist, left should not
	if !fileExists(basePath) {
		t.Error("Base snapshot should exist after initialization")
	}
	if fileExists(leftPath) {
		t.Error("Left snapshot should not exist yet")
	}

	// Capture left snapshot
	if err := captureLeftSnapshot(jsonlPath); err != nil {
		t.Fatalf("Failed to capture left snapshot: %v", err)
	}

	if !fileExists(leftPath) {
		t.Error("Left snapshot should exist after capture")
	}

	// Update base snapshot
	if err := updateBaseSnapshot(jsonlPath); err != nil {
		t.Fatalf("Failed to update base snapshot: %v", err)
	}

	// Both should exist now
	baseCount, leftCount, baseExists, leftExists := getSnapshotStats(jsonlPath)
	if !baseExists || !leftExists {
		t.Error("Both snapshots should exist")
	}
	if baseCount != 1 || leftCount != 1 {
		t.Errorf("Expected 1 issue in each snapshot, got base=%d left=%d", baseCount, leftCount)
	}
}
