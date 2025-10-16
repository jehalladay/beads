package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/steveyegge/beads/internal/storage/sqlite"
	"github.com/steveyegge/beads/internal/types"
)

func TestReopenCommand(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "bd-test-reopen-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	testDB := filepath.Join(tmpDir, "test.db")
	s, err := sqlite.New(testDB)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	ctx := context.Background()

	t.Run("reopen closed issue", func(t *testing.T) {
		issue := &types.Issue{
			Title:       "Test Issue",
			Description: "Test description",
			Priority:    1,
			IssueType:   types.TypeBug,
			Status:      types.StatusOpen,
		}

		if err := s.CreateIssue(ctx, issue, "test-user"); err != nil {
			t.Fatalf("Failed to create issue: %v", err)
		}

		if err := s.CloseIssue(ctx, issue.ID, "test-user", "Closing for test"); err != nil {
			t.Fatalf("Failed to close issue: %v", err)
		}

		closed, err := s.GetIssue(ctx, issue.ID)
		if err != nil {
			t.Fatalf("Failed to get closed issue: %v", err)
		}
		if closed.Status != types.StatusClosed {
			t.Errorf("Expected status to be closed, got %s", closed.Status)
		}
		if closed.ClosedAt == nil {
			t.Error("Expected ClosedAt to be set")
		}

		updates := map[string]interface{}{
			"status": string(types.StatusOpen),
		}
		if err := s.UpdateIssue(ctx, issue.ID, updates, "test-user"); err != nil {
			t.Fatalf("Failed to reopen issue: %v", err)
		}

		reopened, err := s.GetIssue(ctx, issue.ID)
		if err != nil {
			t.Fatalf("Failed to get reopened issue: %v", err)
		}
		if reopened.Status != types.StatusOpen {
			t.Errorf("Expected status to be open, got %s", reopened.Status)
		}
		if reopened.ClosedAt != nil {
			t.Errorf("Expected ClosedAt to be nil, got %v", reopened.ClosedAt)
		}
	})

	t.Run("reopen with reason adds comment", func(t *testing.T) {
		issue := &types.Issue{
			Title:       "Test Issue 2",
			Description: "Test description",
			Priority:    1,
			IssueType:   types.TypeTask,
			Status:      types.StatusOpen,
		}

		if err := s.CreateIssue(ctx, issue, "test-user"); err != nil {
			t.Fatalf("Failed to create issue: %v", err)
		}

		if err := s.CloseIssue(ctx, issue.ID, "test-user", "Done"); err != nil {
			t.Fatalf("Failed to close issue: %v", err)
		}

		updates := map[string]interface{}{
			"status": string(types.StatusOpen),
		}
		if err := s.UpdateIssue(ctx, issue.ID, updates, "test-user"); err != nil {
			t.Fatalf("Failed to reopen issue: %v", err)
		}

		reason := "Found a regression"
		if err := s.AddComment(ctx, issue.ID, "test-user", reason); err != nil {
			t.Fatalf("Failed to add comment: %v", err)
		}

		events, err := s.GetEvents(ctx, issue.ID, 100)
		if err != nil {
			t.Fatalf("Failed to get events: %v", err)
		}

		found := false
		for _, e := range events {
			if e.EventType == types.EventCommented && e.Comment != nil && *e.Comment == reason {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Expected to find comment event with reason '%s'", reason)
		}
	})

	t.Run("reopen multiple issues", func(t *testing.T) {
		issue1 := &types.Issue{
			Title:     "Multi Test 1",
			Priority:  1,
			IssueType: types.TypeBug,
			Status:    types.StatusOpen,
		}
		issue2 := &types.Issue{
			Title:     "Multi Test 2",
			Priority:  1,
			IssueType: types.TypeBug,
			Status:    types.StatusOpen,
		}

		if err := s.CreateIssue(ctx, issue1, "test-user"); err != nil {
			t.Fatalf("Failed to create issue1: %v", err)
		}
		if err := s.CreateIssue(ctx, issue2, "test-user"); err != nil {
			t.Fatalf("Failed to create issue2: %v", err)
		}

		if err := s.CloseIssue(ctx, issue1.ID, "test-user", "Done"); err != nil {
			t.Fatalf("Failed to close issue1: %v", err)
		}
		if err := s.CloseIssue(ctx, issue2.ID, "test-user", "Done"); err != nil {
			t.Fatalf("Failed to close issue2: %v", err)
		}

		updates1 := map[string]interface{}{
			"status": string(types.StatusOpen),
		}
		if err := s.UpdateIssue(ctx, issue1.ID, updates1, "test-user"); err != nil {
			t.Fatalf("Failed to reopen issue1: %v", err)
		}

		updates2 := map[string]interface{}{
			"status": string(types.StatusOpen),
		}
		if err := s.UpdateIssue(ctx, issue2.ID, updates2, "test-user"); err != nil {
			t.Fatalf("Failed to reopen issue2: %v", err)
		}

		reopened1, err := s.GetIssue(ctx, issue1.ID)
		if err != nil {
			t.Fatalf("Failed to get issue1: %v", err)
		}
		reopened2, err := s.GetIssue(ctx, issue2.ID)
		if err != nil {
			t.Fatalf("Failed to get issue2: %v", err)
		}

		if reopened1.Status != types.StatusOpen {
			t.Errorf("Expected issue1 status to be open, got %s", reopened1.Status)
		}
		if reopened2.Status != types.StatusOpen {
			t.Errorf("Expected issue2 status to be open, got %s", reopened2.Status)
		}
	})

	t.Run("reopen already open issue is no-op", func(t *testing.T) {
		issue := &types.Issue{
			Title:     "Already Open",
			Priority:  1,
			IssueType: types.TypeTask,
			Status:    types.StatusOpen,
		}

		if err := s.CreateIssue(ctx, issue, "test-user"); err != nil {
			t.Fatalf("Failed to create issue: %v", err)
		}

		updates := map[string]interface{}{
			"status": string(types.StatusOpen),
		}
		if err := s.UpdateIssue(ctx, issue.ID, updates, "test-user"); err != nil {
			t.Fatalf("Failed to update issue: %v", err)
		}

		updated, err := s.GetIssue(ctx, issue.ID)
		if err != nil {
			t.Fatalf("Failed to get issue: %v", err)
		}
		if updated.Status != types.StatusOpen {
			t.Errorf("Expected status to remain open, got %s", updated.Status)
		}
		if updated.ClosedAt != nil {
			t.Errorf("Expected ClosedAt to remain nil, got %v", updated.ClosedAt)
		}
	})
}
