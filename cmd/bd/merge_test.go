package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/steveyegge/beads/internal/storage/sqlite"
	"github.com/steveyegge/beads/internal/types"
)

func TestValidateMerge(t *testing.T) {
	tmpDir := t.TempDir()
	dbFile := filepath.Join(tmpDir, ".beads", "issues.db")
	if err := os.MkdirAll(filepath.Dir(dbFile), 0755); err != nil {
		t.Fatalf("Failed to create test directory: %v", err)
	}

	testStore, err := sqlite.New(dbFile)
	if err != nil {
		t.Fatalf("Failed to create test storage: %v", err)
	}
	defer testStore.Close()

	store = testStore
	ctx := context.Background()

	// Create test issues
	issue1 := &types.Issue{
		ID:          "bd-1",
		Title:       "Test issue 1",
		Description: "Test",
		Priority:    1,
		IssueType:   types.TypeTask,
		Status:      types.StatusOpen,
	}
	issue2 := &types.Issue{
		ID:          "bd-2",
		Title:       "Test issue 2",
		Description: "Test",
		Priority:    1,
		IssueType:   types.TypeTask,
		Status:      types.StatusOpen,
	}
	issue3 := &types.Issue{
		ID:          "bd-3",
		Title:       "Test issue 3",
		Description: "Test",
		Priority:    1,
		IssueType:   types.TypeTask,
		Status:      types.StatusOpen,
	}

	if err := testStore.CreateIssue(ctx, issue1, "test"); err != nil {
		t.Fatalf("Failed to create issue1: %v", err)
	}
	if err := testStore.CreateIssue(ctx, issue2, "test"); err != nil {
		t.Fatalf("Failed to create issue2: %v", err)
	}
	if err := testStore.CreateIssue(ctx, issue3, "test"); err != nil {
		t.Fatalf("Failed to create issue3: %v", err)
	}

	tests := []struct {
		name      string
		targetID  string
		sourceIDs []string
		wantErr   bool
		errMsg    string
	}{
		{
			name:      "valid merge",
			targetID:  "bd-1",
			sourceIDs: []string{"bd-2", "bd-3"},
			wantErr:   false,
		},
		{
			name:      "self-merge error",
			targetID:  "bd-1",
			sourceIDs: []string{"bd-1"},
			wantErr:   true,
			errMsg:    "cannot merge issue into itself",
		},
		{
			name:      "self-merge in list",
			targetID:  "bd-1",
			sourceIDs: []string{"bd-2", "bd-1"},
			wantErr:   true,
			errMsg:    "cannot merge issue into itself",
		},
		{
			name:      "nonexistent target",
			targetID:  "bd-999",
			sourceIDs: []string{"bd-1"},
			wantErr:   true,
			errMsg:    "target issue not found",
		},
		{
			name:      "nonexistent source",
			targetID:  "bd-1",
			sourceIDs: []string{"bd-999"},
			wantErr:   true,
			errMsg:    "source issue not found",
		},
		{
			name:      "multiple sources valid",
			targetID:  "bd-1",
			sourceIDs: []string{"bd-2"},
			wantErr:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateMerge(tt.targetID, tt.sourceIDs)
			if tt.wantErr {
				if err == nil {
					t.Errorf("validateMerge() expected error, got nil")
				} else if tt.errMsg != "" && !contains(err.Error(), tt.errMsg) {
					t.Errorf("validateMerge() error = %v, want error containing %v", err, tt.errMsg)
				}
			} else {
				if err != nil {
					t.Errorf("validateMerge() unexpected error: %v", err)
				}
			}
		})
	}
}

func TestValidateMergeMultipleSelfReferences(t *testing.T) {
	tmpDir := t.TempDir()
	dbFile := filepath.Join(tmpDir, ".beads", "issues.db")
	if err := os.MkdirAll(filepath.Dir(dbFile), 0755); err != nil {
		t.Fatalf("Failed to create test directory: %v", err)
	}

	testStore, err := sqlite.New(dbFile)
	if err != nil {
		t.Fatalf("Failed to create test storage: %v", err)
	}
	defer testStore.Close()

	store = testStore
	ctx := context.Background()

	issue1 := &types.Issue{
		ID:          "bd-10",
		Title:       "Test issue 10",
		Description: "Test",
		Priority:    1,
		IssueType:   types.TypeTask,
		Status:      types.StatusOpen,
	}

	if err := testStore.CreateIssue(ctx, issue1, "test"); err != nil {
		t.Fatalf("Failed to create issue: %v", err)
	}

	// Test merging multiple instances of same ID (should catch first one)
	err = validateMerge("bd-10", []string{"bd-10", "bd-10"})
	if err == nil {
		t.Error("validateMerge() expected error for duplicate self-merge, got nil")
	}
	if !contains(err.Error(), "cannot merge issue into itself") {
		t.Errorf("validateMerge() error = %v, want error containing 'cannot merge issue into itself'", err)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && containsSubstring(s, substr))
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
