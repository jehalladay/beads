package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/storage/sqlite"
	"github.com/steveyegge/beads/internal/types"
)

// TestIdempotentImportNoTimestampChurn verifies that importing unchanged issues
// does not update their timestamps (bd-84)
func TestIdempotentImportNoTimestampChurn(t *testing.T) {
	// Create temp directory
	tmpDir, err := os.MkdirTemp("", "bd-test-idempotent-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath = filepath.Join(tmpDir, "test.db")
	jsonlPath := filepath.Join(tmpDir, "issues.jsonl")

	// Create store
	testStore, err := sqlite.New(dbPath)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer testStore.Close()

	store = testStore
	storeMutex.Lock()
	storeActive = true
	storeMutex.Unlock()
	defer func() {
		storeMutex.Lock()
		storeActive = false
		storeMutex.Unlock()
	}()

	ctx := context.Background()

	// Create an issue
	issue := &types.Issue{
		ID:          "bd-1",
		Title:       "Test Issue",
		Description: "Test description",
		Status:      types.StatusOpen,
		Priority:    2,
		IssueType:   types.TypeTask,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	if err := testStore.CreateIssue(ctx, issue, "test"); err != nil {
		t.Fatalf("Failed to create issue: %v", err)
	}

	// Get initial timestamp
	issue1, err := testStore.GetIssue(ctx, "bd-1")
	if err != nil {
		t.Fatalf("Failed to get issue: %v", err)
	}
	initialUpdatedAt := issue1.UpdatedAt

	// Export to JSONL
	f, err := os.Create(jsonlPath)
	if err != nil {
		t.Fatalf("Failed to create JSONL: %v", err)
	}
	encoder := json.NewEncoder(f)
	if err := encoder.Encode(issue1); err != nil {
		t.Fatalf("Failed to encode issue: %v", err)
	}
	f.Close()

	// Wait a bit to ensure timestamps would be different if updated
	time.Sleep(100 * time.Millisecond)

	// Import the same JSONL (should be idempotent)
	autoImportIfNewer()

	// Get issue again
	issue2, err := testStore.GetIssue(ctx, "bd-1")
	if err != nil {
		t.Fatalf("Failed to get issue after import: %v", err)
	}

	// Verify timestamp was NOT updated
	if !issue2.UpdatedAt.Equal(initialUpdatedAt) {
		t.Errorf("Import updated timestamp even though data unchanged!\n"+
			"Before: %v\nAfter:  %v",
			initialUpdatedAt, issue2.UpdatedAt)
	}
}

// TestImportMultipleUnchangedIssues verifies that importing multiple unchanged issues
// does not update any of their timestamps (bd-84)
func TestImportMultipleUnchangedIssues(t *testing.T) {
	// Create temp directory
	tmpDir, err := os.MkdirTemp("", "bd-test-changed-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath = filepath.Join(tmpDir, "test.db")
	jsonlPath := filepath.Join(tmpDir, "issues.jsonl")

	// Create store
	testStore, err := sqlite.New(dbPath)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer testStore.Close()

	store = testStore
	storeMutex.Lock()
	storeActive = true
	storeMutex.Unlock()
	defer func() {
		storeMutex.Lock()
		storeActive = false
		storeMutex.Unlock()
	}()

	ctx := context.Background()

	// Create two issues
	issue1 := &types.Issue{
		ID:          "bd-1",
		Title:       "Unchanged Issue",
		Description: "Will not change",
		Status:      types.StatusOpen,
		Priority:    2,
		IssueType:   types.TypeTask,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	issue2 := &types.Issue{
		ID:          "bd-2",
		Title:       "Changed Issue",
		Description: "Will change",
		Status:      types.StatusOpen,
		Priority:    2,
		IssueType:   types.TypeTask,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	if err := testStore.CreateIssue(ctx, issue1, "test"); err != nil {
		t.Fatalf("Failed to create issue 1: %v", err)
	}
	if err := testStore.CreateIssue(ctx, issue2, "test"); err != nil {
		t.Fatalf("Failed to create issue 2: %v", err)
	}

	// Get initial timestamps
	unchanged, _ := testStore.GetIssue(ctx, "bd-1")
	changed, _ := testStore.GetIssue(ctx, "bd-2")
	unchangedInitialTS := unchanged.UpdatedAt
	changedInitialTS := changed.UpdatedAt

	// Export both issues to JSONL (unchanged)
	f, err := os.Create(jsonlPath)
	if err != nil {
		t.Fatalf("Failed to create JSONL: %v", err)
	}
	encoder := json.NewEncoder(f)
	if err := encoder.Encode(unchanged); err != nil {
		t.Fatalf("Failed to encode issue 1: %v", err)
	}
	if err := encoder.Encode(changed); err != nil {
		t.Fatalf("Failed to encode issue 2: %v", err)
	}
	f.Close()

	// Wait to ensure timestamps would differ if updated
	time.Sleep(100 * time.Millisecond)

	// Import same JSONL (both issues unchanged - should be idempotent)
	autoImportIfNewer()

	// Check timestamps - neither should have changed
	issue1After, _ := testStore.GetIssue(ctx, "bd-1")
	issue2After, _ := testStore.GetIssue(ctx, "bd-2")

	// bd-1 should have same timestamp
	if !issue1After.UpdatedAt.Equal(unchangedInitialTS) {
		t.Errorf("bd-1 timestamp changed even though issue unchanged!\n"+
			"Before: %v\nAfter:  %v",
			unchangedInitialTS, issue1After.UpdatedAt)
	}

	// bd-2 should also have same timestamp
	if !issue2After.UpdatedAt.Equal(changedInitialTS) {
		t.Errorf("bd-2 timestamp changed even though issue unchanged!\n"+
			"Before: %v\nAfter:  %v",
			changedInitialTS, issue2After.UpdatedAt)
	}
}
