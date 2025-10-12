package sqlite

import (
	"context"
	"fmt"

	"github.com/steveyegge/beads/internal/types"
)

// CollisionResult categorizes incoming issues by their relationship to existing DB state
type CollisionResult struct {
	ExactMatches []string           // IDs that match exactly (idempotent import)
	Collisions   []*CollisionDetail // Issues with same ID but different content
	NewIssues    []string           // IDs that don't exist in DB yet
}

// CollisionDetail provides detailed information about a collision
type CollisionDetail struct {
	ID                string        // The issue ID that collided
	IncomingIssue     *types.Issue  // The issue from the import file
	ExistingIssue     *types.Issue  // The issue currently in the database
	ConflictingFields []string      // List of field names that differ
}

// detectCollisions compares incoming JSONL issues against DB state
// It distinguishes between:
//  1. Exact match (idempotent) - ID and content are identical
//  2. ID match but different content (collision) - same ID, different fields
//  3. New issue - ID doesn't exist in DB
//
// Returns a CollisionResult categorizing all incoming issues.
func detectCollisions(ctx context.Context, s *SQLiteStorage, incomingIssues []*types.Issue) (*CollisionResult, error) {
	result := &CollisionResult{
		ExactMatches: make([]string, 0),
		Collisions:   make([]*CollisionDetail, 0),
		NewIssues:    make([]string, 0),
	}

	for _, incoming := range incomingIssues {
		// Check if issue exists in database
		existing, err := s.GetIssue(ctx, incoming.ID)
		if err != nil {
			return nil, fmt.Errorf("failed to check issue %s: %w", incoming.ID, err)
		}

		if existing == nil {
			// Issue doesn't exist in DB - it's new
			result.NewIssues = append(result.NewIssues, incoming.ID)
			continue
		}

		// Issue exists - compare content
		conflicts := compareIssues(existing, incoming)
		if len(conflicts) == 0 {
			// No differences - exact match (idempotent)
			result.ExactMatches = append(result.ExactMatches, incoming.ID)
		} else {
			// Same ID but different content - collision
			result.Collisions = append(result.Collisions, &CollisionDetail{
				ID:                incoming.ID,
				IncomingIssue:     incoming,
				ExistingIssue:     existing,
				ConflictingFields: conflicts,
			})
		}
	}

	return result, nil
}

// compareIssues compares two issues and returns a list of field names that differ
// Timestamps (CreatedAt, UpdatedAt, ClosedAt) are intentionally not compared
// Dependencies are also not compared (handled separately in import)
func compareIssues(existing, incoming *types.Issue) []string {
	conflicts := make([]string, 0)

	// Compare all relevant fields
	if existing.Title != incoming.Title {
		conflicts = append(conflicts, "title")
	}
	if existing.Description != incoming.Description {
		conflicts = append(conflicts, "description")
	}
	if existing.Design != incoming.Design {
		conflicts = append(conflicts, "design")
	}
	if existing.AcceptanceCriteria != incoming.AcceptanceCriteria {
		conflicts = append(conflicts, "acceptance_criteria")
	}
	if existing.Notes != incoming.Notes {
		conflicts = append(conflicts, "notes")
	}
	if existing.Status != incoming.Status {
		conflicts = append(conflicts, "status")
	}
	if existing.Priority != incoming.Priority {
		conflicts = append(conflicts, "priority")
	}
	if existing.IssueType != incoming.IssueType {
		conflicts = append(conflicts, "issue_type")
	}
	if existing.Assignee != incoming.Assignee {
		conflicts = append(conflicts, "assignee")
	}

	// Compare EstimatedMinutes (handle nil cases)
	if !equalIntPtr(existing.EstimatedMinutes, incoming.EstimatedMinutes) {
		conflicts = append(conflicts, "estimated_minutes")
	}

	return conflicts
}

// equalIntPtr compares two *int pointers for equality
func equalIntPtr(a, b *int) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}
