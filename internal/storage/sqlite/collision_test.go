package sqlite

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

func TestDetectCollisions(t *testing.T) {
	// Create temporary database
	tmpDir, err := os.MkdirTemp("", "collision-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.db")
	store, err := New(dbPath)
	if err != nil {
		t.Fatalf("failed to create storage: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	// Setup: Create some existing issues in the database
	existingIssue1 := &types.Issue{
		ID:          "bd-1",
		Title:       "Existing issue 1",
		Description: "This is an existing issue",
		Status:      types.StatusOpen,
		Priority:    1,
		IssueType:   types.TypeTask,
	}

	existingIssue2 := &types.Issue{
		ID:          "bd-2",
		Title:       "Existing issue 2",
		Description: "Another existing issue",
		Status:      types.StatusInProgress,
		Priority:    2,
		IssueType:   types.TypeBug,
	}

	if err := store.CreateIssue(ctx, existingIssue1, "test"); err != nil {
		t.Fatalf("failed to create existing issue 1: %v", err)
	}
	if err := store.CreateIssue(ctx, existingIssue2, "test"); err != nil {
		t.Fatalf("failed to create existing issue 2: %v", err)
	}

	// Test cases
	tests := []struct {
		name              string
		incomingIssues    []*types.Issue
		expectedExact     int
		expectedCollision int
		expectedNew       int
		checkCollisions   func(t *testing.T, collisions []*CollisionDetail)
	}{
		{
			name: "exact match - idempotent import",
			incomingIssues: []*types.Issue{
				{
					ID:          "bd-1",
					Title:       "Existing issue 1",
					Description: "This is an existing issue",
					Status:      types.StatusOpen,
					Priority:    1,
					IssueType:   types.TypeTask,
				},
			},
			expectedExact:     1,
			expectedCollision: 0,
			expectedNew:       0,
		},
		{
			name: "new issue - doesn't exist in DB",
			incomingIssues: []*types.Issue{
				{
					ID:          "bd-100",
					Title:       "Brand new issue",
					Description: "This doesn't exist yet",
					Status:      types.StatusOpen,
					Priority:    1,
					IssueType:   types.TypeFeature,
				},
			},
			expectedExact:     0,
			expectedCollision: 0,
			expectedNew:       1,
		},
		{
			name: "collision - same ID, different title",
			incomingIssues: []*types.Issue{
				{
					ID:          "bd-1",
					Title:       "Modified title",
					Description: "This is an existing issue",
					Status:      types.StatusOpen,
					Priority:    1,
					IssueType:   types.TypeTask,
				},
			},
			expectedExact:     0,
			expectedCollision: 1,
			expectedNew:       0,
			checkCollisions: func(t *testing.T, collisions []*CollisionDetail) {
				if len(collisions) != 1 {
					t.Fatalf("expected 1 collision, got %d", len(collisions))
				}
				if collisions[0].ID != "bd-1" {
					t.Errorf("expected collision ID bd-1, got %s", collisions[0].ID)
				}
				if len(collisions[0].ConflictingFields) != 1 {
					t.Errorf("expected 1 conflicting field, got %d", len(collisions[0].ConflictingFields))
				}
				if collisions[0].ConflictingFields[0] != "title" {
					t.Errorf("expected conflicting field 'title', got %s", collisions[0].ConflictingFields[0])
				}
			},
		},
		{
			name: "collision - multiple fields differ",
			incomingIssues: []*types.Issue{
				{
					ID:          "bd-2",
					Title:       "Changed title",
					Description: "Changed description",
					Status:      types.StatusClosed,
					Priority:    3,
					IssueType:   types.TypeFeature,
				},
			},
			expectedExact:     0,
			expectedCollision: 1,
			expectedNew:       0,
			checkCollisions: func(t *testing.T, collisions []*CollisionDetail) {
				if len(collisions) != 1 {
					t.Fatalf("expected 1 collision, got %d", len(collisions))
				}
				// Should have multiple conflicting fields
				expectedFields := map[string]bool{
					"title":       true,
					"description": true,
					"status":      true,
					"priority":    true,
					"issue_type":  true,
				}
				for _, field := range collisions[0].ConflictingFields {
					if !expectedFields[field] {
						t.Errorf("unexpected conflicting field: %s", field)
					}
					delete(expectedFields, field)
				}
				if len(expectedFields) > 0 {
					t.Errorf("missing expected conflicting fields: %v", expectedFields)
				}
			},
		},
		{
			name: "mixed - exact, collision, and new",
			incomingIssues: []*types.Issue{
				{
					// Exact match
					ID:          "bd-1",
					Title:       "Existing issue 1",
					Description: "This is an existing issue",
					Status:      types.StatusOpen,
					Priority:    1,
					IssueType:   types.TypeTask,
				},
				{
					// Collision
					ID:          "bd-2",
					Title:       "Modified issue 2",
					Description: "Another existing issue",
					Status:      types.StatusInProgress,
					Priority:    2,
					IssueType:   types.TypeBug,
				},
				{
					// New issue
					ID:          "bd-200",
					Title:       "New issue",
					Description: "This is new",
					Status:      types.StatusOpen,
					Priority:    1,
					IssueType:   types.TypeTask,
				},
			},
			expectedExact:     1,
			expectedCollision: 1,
			expectedNew:       1,
		},
		{
			name: "collision - estimated_minutes differs",
			incomingIssues: []*types.Issue{
				{
					ID:               "bd-1",
					Title:            "Existing issue 1",
					Description:      "This is an existing issue",
					Status:           types.StatusOpen,
					Priority:         1,
					IssueType:        types.TypeTask,
					EstimatedMinutes: intPtr(60),
				},
			},
			expectedExact:     0,
			expectedCollision: 1,
			expectedNew:       0,
			checkCollisions: func(t *testing.T, collisions []*CollisionDetail) {
				if len(collisions[0].ConflictingFields) != 1 {
					t.Errorf("expected 1 conflicting field, got %d", len(collisions[0].ConflictingFields))
				}
				if collisions[0].ConflictingFields[0] != "estimated_minutes" {
					t.Errorf("expected conflicting field 'estimated_minutes', got %s", collisions[0].ConflictingFields[0])
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := detectCollisions(ctx, store, tt.incomingIssues)
			if err != nil {
				t.Fatalf("detectCollisions failed: %v", err)
			}

			if len(result.ExactMatches) != tt.expectedExact {
				t.Errorf("expected %d exact matches, got %d", tt.expectedExact, len(result.ExactMatches))
			}
			if len(result.Collisions) != tt.expectedCollision {
				t.Errorf("expected %d collisions, got %d", tt.expectedCollision, len(result.Collisions))
			}
			if len(result.NewIssues) != tt.expectedNew {
				t.Errorf("expected %d new issues, got %d", tt.expectedNew, len(result.NewIssues))
			}

			if tt.checkCollisions != nil {
				tt.checkCollisions(t, result.Collisions)
			}
		})
	}
}

func TestCompareIssues(t *testing.T) {
	tests := []struct {
		name     string
		existing *types.Issue
		incoming *types.Issue
		expected []string
	}{
		{
			name: "identical issues",
			existing: &types.Issue{
				ID:          "bd-1",
				Title:       "Test",
				Description: "Test desc",
				Status:      types.StatusOpen,
				Priority:    1,
				IssueType:   types.TypeTask,
			},
			incoming: &types.Issue{
				ID:          "bd-1",
				Title:       "Test",
				Description: "Test desc",
				Status:      types.StatusOpen,
				Priority:    1,
				IssueType:   types.TypeTask,
			},
			expected: []string{},
		},
		{
			name: "different title",
			existing: &types.Issue{
				ID:          "bd-1",
				Title:       "Original",
				Description: "Test",
				Status:      types.StatusOpen,
				Priority:    1,
				IssueType:   types.TypeTask,
			},
			incoming: &types.Issue{
				ID:          "bd-1",
				Title:       "Modified",
				Description: "Test",
				Status:      types.StatusOpen,
				Priority:    1,
				IssueType:   types.TypeTask,
			},
			expected: []string{"title"},
		},
		{
			name: "different status and priority",
			existing: &types.Issue{
				ID:          "bd-1",
				Title:       "Test",
				Description: "Test",
				Status:      types.StatusOpen,
				Priority:    1,
				IssueType:   types.TypeTask,
			},
			incoming: &types.Issue{
				ID:          "bd-1",
				Title:       "Test",
				Description: "Test",
				Status:      types.StatusClosed,
				Priority:    3,
				IssueType:   types.TypeTask,
			},
			expected: []string{"status", "priority"},
		},
		{
			name: "estimated_minutes - both nil",
			existing: &types.Issue{
				ID:               "bd-1",
				Title:            "Test",
				Description:      "Test",
				Status:           types.StatusOpen,
				Priority:         1,
				IssueType:        types.TypeTask,
				EstimatedMinutes: nil,
			},
			incoming: &types.Issue{
				ID:               "bd-1",
				Title:            "Test",
				Description:      "Test",
				Status:           types.StatusOpen,
				Priority:         1,
				IssueType:        types.TypeTask,
				EstimatedMinutes: nil,
			},
			expected: []string{},
		},
		{
			name: "estimated_minutes - existing nil, incoming set",
			existing: &types.Issue{
				ID:               "bd-1",
				Title:            "Test",
				Description:      "Test",
				Status:           types.StatusOpen,
				Priority:         1,
				IssueType:        types.TypeTask,
				EstimatedMinutes: nil,
			},
			incoming: &types.Issue{
				ID:               "bd-1",
				Title:            "Test",
				Description:      "Test",
				Status:           types.StatusOpen,
				Priority:         1,
				IssueType:        types.TypeTask,
				EstimatedMinutes: intPtr(30),
			},
			expected: []string{"estimated_minutes"},
		},
		{
			name: "estimated_minutes - same values",
			existing: &types.Issue{
				ID:               "bd-1",
				Title:            "Test",
				Description:      "Test",
				Status:           types.StatusOpen,
				Priority:         1,
				IssueType:        types.TypeTask,
				EstimatedMinutes: intPtr(60),
			},
			incoming: &types.Issue{
				ID:               "bd-1",
				Title:            "Test",
				Description:      "Test",
				Status:           types.StatusOpen,
				Priority:         1,
				IssueType:        types.TypeTask,
				EstimatedMinutes: intPtr(60),
			},
			expected: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conflicts := compareIssues(tt.existing, tt.incoming)
			if len(conflicts) != len(tt.expected) {
				t.Errorf("expected %d conflicts, got %d: %v", len(tt.expected), len(conflicts), conflicts)
				return
			}
			for i, expected := range tt.expected {
				if conflicts[i] != expected {
					t.Errorf("conflict[%d]: expected %s, got %s", i, expected, conflicts[i])
				}
			}
		})
	}
}

func TestEqualIntPtr(t *testing.T) {
	tests := []struct {
		name     string
		a        *int
		b        *int
		expected bool
	}{
		{"both nil", nil, nil, true},
		{"a nil, b set", nil, intPtr(5), false},
		{"a set, b nil", intPtr(5), nil, false},
		{"same values", intPtr(10), intPtr(10), true},
		{"different values", intPtr(10), intPtr(20), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := equalIntPtr(tt.a, tt.b)
			if result != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, result)
			}
		})
	}
}

// Helper function to create *int from int value
func intPtr(i int) *int {
	return &i
}
