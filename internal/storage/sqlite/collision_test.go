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

func TestCountReferences(t *testing.T) {
	allIssues := []*types.Issue{
		{
			ID:          "bd-1",
			Title:       "Issue 1",
			Description: "This mentions bd-2 and bd-3",
			Design:      "Design mentions bd-2 twice: bd-2 and bd-2",
			Notes:       "Notes mention bd-3",
		},
		{
			ID:          "bd-2",
			Title:       "Issue 2",
			Description: "This mentions bd-1",
		},
		{
			ID:          "bd-3",
			Title:       "Issue 3",
			Description: "No mentions here",
		},
		{
			ID:          "bd-10",
			Title:       "Issue 10",
			Description: "This has bd-100 but not bd-10 itself",
		},
	}

	allDeps := map[string][]*types.Dependency{
		"bd-1": {
			{IssueID: "bd-1", DependsOnID: "bd-2", Type: types.DepBlocks},
		},
		"bd-2": {
			{IssueID: "bd-2", DependsOnID: "bd-3", Type: types.DepBlocks},
		},
	}

	tests := []struct {
		name          string
		issueID       string
		expectedCount int
	}{
		{
			name:    "bd-1 - one text mention, one dependency",
			issueID: "bd-1",
			// Text: bd-2's description mentions bd-1 (1)
			// Deps: bd-1 → bd-2 (1)
			expectedCount: 2,
		},
		{
			name:    "bd-2 - multiple text mentions, two dependencies",
			issueID: "bd-2",
			// Text: bd-1's description mentions bd-2 (1) + bd-1's design mentions bd-2 three times (3) = 4
			//       (design has: "mentions bd-2" + "bd-2 and" + "bd-2")
			// Deps: bd-1 → bd-2 (1) + bd-2 → bd-3 (1) = 2
			expectedCount: 6,
		},
		{
			name:    "bd-3 - some text mentions, one dependency",
			issueID: "bd-3",
			// Text: bd-1's description (1) + bd-1's notes (1) = 2
			// Deps: bd-2 → bd-3 (1)
			expectedCount: 3,
		},
		{
			name:    "bd-10 - no mentions (bd-100 doesn't count)",
			issueID: "bd-10",
			// Text: bd-100 in bd-10's description doesn't match \bbd-10\b = 0
			// Deps: none = 0
			expectedCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			count, err := countReferences(tt.issueID, allIssues, allDeps)
			if err != nil {
				t.Fatalf("countReferences failed: %v", err)
			}
			if count != tt.expectedCount {
				t.Errorf("expected count %d, got %d", tt.expectedCount, count)
			}
		})
	}
}

func TestScoreCollisions(t *testing.T) {
	// Create temporary database
	tmpDir, err := os.MkdirTemp("", "score-collision-test-*")
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

	// Setup: Create issues with various reference patterns
	issue1 := &types.Issue{
		ID:          "bd-1",
		Title:       "Issue 1",
		Description: "Depends on bd-2",
		Status:      types.StatusOpen,
		Priority:    1,
		IssueType:   types.TypeTask,
	}

	issue2 := &types.Issue{
		ID:          "bd-2",
		Title:       "Issue 2",
		Description: "Referenced by bd-1 and bd-3",
		Status:      types.StatusOpen,
		Priority:    1,
		IssueType:   types.TypeTask,
	}

	issue3 := &types.Issue{
		ID:          "bd-3",
		Title:       "Issue 3",
		Description: "Mentions bd-2 multiple times: bd-2 and bd-2",
		Notes:       "Also mentions bd-2 here",
		Status:      types.StatusOpen,
		Priority:    1,
		IssueType:   types.TypeTask,
	}

	issue4 := &types.Issue{
		ID:          "bd-4",
		Title:       "Issue 4",
		Description: "Lonely issue with no references",
		Status:      types.StatusOpen,
		Priority:    1,
		IssueType:   types.TypeTask,
	}

	// Create issues in DB
	if err := store.CreateIssue(ctx, issue1, "test"); err != nil {
		t.Fatalf("failed to create issue1: %v", err)
	}
	if err := store.CreateIssue(ctx, issue2, "test"); err != nil {
		t.Fatalf("failed to create issue2: %v", err)
	}
	if err := store.CreateIssue(ctx, issue3, "test"); err != nil {
		t.Fatalf("failed to create issue3: %v", err)
	}
	if err := store.CreateIssue(ctx, issue4, "test"); err != nil {
		t.Fatalf("failed to create issue4: %v", err)
	}

	// Add dependencies
	dep1 := &types.Dependency{IssueID: "bd-1", DependsOnID: "bd-2", Type: types.DepBlocks}
	dep2 := &types.Dependency{IssueID: "bd-3", DependsOnID: "bd-2", Type: types.DepBlocks}

	if err := store.AddDependency(ctx, dep1, "test"); err != nil {
		t.Fatalf("failed to add dependency1: %v", err)
	}
	if err := store.AddDependency(ctx, dep2, "test"); err != nil {
		t.Fatalf("failed to add dependency2: %v", err)
	}

	// Create collision details (simulated)
	collisions := []*CollisionDetail{
		{
			ID:             "bd-1",
			IncomingIssue:  issue1,
			ExistingIssue:  issue1,
			ReferenceScore: 0, // Will be calculated
		},
		{
			ID:             "bd-2",
			IncomingIssue:  issue2,
			ExistingIssue:  issue2,
			ReferenceScore: 0, // Will be calculated
		},
		{
			ID:             "bd-3",
			IncomingIssue:  issue3,
			ExistingIssue:  issue3,
			ReferenceScore: 0, // Will be calculated
		},
		{
			ID:             "bd-4",
			IncomingIssue:  issue4,
			ExistingIssue:  issue4,
			ReferenceScore: 0, // Will be calculated
		},
	}

	allIssues := []*types.Issue{issue1, issue2, issue3, issue4}

	// Score the collisions
	err = scoreCollisions(ctx, store, collisions, allIssues)
	if err != nil {
		t.Fatalf("scoreCollisions failed: %v", err)
	}

	// Verify scores were calculated
	// bd-4: 0 references (no mentions, no deps)
	// bd-1: 1 reference (bd-1 → bd-2 dependency)
	// bd-3: 1 reference (bd-3 → bd-2 dependency)
	// bd-2: high references (mentioned in bd-1, bd-3 multiple times + 2 deps as target)
	//       bd-1 desc (1) + bd-3 desc (3: "bd-2 multiple", "bd-2 and", "bd-2") + bd-3 notes (1) + 2 deps = 7

	if collisions[0].ID != "bd-4" {
		t.Errorf("expected first collision to be bd-4 (lowest score), got %s", collisions[0].ID)
	}
	if collisions[0].ReferenceScore != 0 {
		t.Errorf("expected bd-4 to have score 0, got %d", collisions[0].ReferenceScore)
	}

	// bd-2 should be last (highest score)
	lastIdx := len(collisions) - 1
	if collisions[lastIdx].ID != "bd-2" {
		t.Errorf("expected last collision to be bd-2 (highest score), got %s", collisions[lastIdx].ID)
	}
	if collisions[lastIdx].ReferenceScore != 7 {
		t.Errorf("expected bd-2 to have score 7, got %d", collisions[lastIdx].ReferenceScore)
	}

	// Verify sorting (ascending order)
	for i := 1; i < len(collisions); i++ {
		if collisions[i].ReferenceScore < collisions[i-1].ReferenceScore {
			t.Errorf("collisions not sorted: collision[%d] score %d < collision[%d] score %d",
				i, collisions[i].ReferenceScore, i-1, collisions[i-1].ReferenceScore)
		}
	}
}

func TestCountReferencesWordBoundary(t *testing.T) {
	// Test that word boundaries work correctly
	allIssues := []*types.Issue{
		{
			ID:          "bd-1",
			Description: "bd-10 and bd-100 and bd-1 and bd-11",
		},
		{
			ID:          "bd-10",
			Description: "bd-1 and bd-100",
		},
	}

	allDeps := map[string][]*types.Dependency{}

	tests := []struct {
		name          string
		issueID       string
		expectedCount int
		description   string
	}{
		{
			name:          "bd-1 exact match",
			issueID:       "bd-1",
			expectedCount: 2, // bd-10's desc mentions bd-1 (1) + bd-1's desc mentions bd-1 (1) = 2
			// Wait, bd-1's desc shouldn't count itself
			// So: bd-10's desc mentions bd-1 (1)
		},
		{
			name:          "bd-10 exact match",
			issueID:       "bd-10",
			expectedCount: 1, // bd-1's desc mentions bd-10 (1)
		},
		{
			name:          "bd-100 exact match",
			issueID:       "bd-100",
			expectedCount: 2, // bd-1's desc (1) + bd-10's desc (1)
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			count, err := countReferences(tt.issueID, allIssues, allDeps)
			if err != nil {
				t.Fatalf("countReferences failed: %v", err)
			}

			// Adjust expected based on actual counting logic
			// countReferences skips the issue itself
			expected := tt.expectedCount
			if tt.issueID == "bd-1" {
				expected = 1 // only bd-10's description
			}

			if count != expected {
				t.Errorf("expected count %d, got %d", expected, count)
			}
		})
	}
}
