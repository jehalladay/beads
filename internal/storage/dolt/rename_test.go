package dolt

import (
	"context"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

func TestUpdateIssueIDUpdatesWispTables(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()

	// Create a permanent issue that we'll rename
	issue := &types.Issue{
		ID:        "test-old1",
		Title:     "Test issue",
		Status:    types.StatusOpen,
		Priority:  2,
		IssueType: types.TypeTask,
	}
	if err := store.CreateIssue(ctx, issue, "test"); err != nil {
		t.Fatalf("failed to create issue: %v", err)
	}

	// Create a wisp that references the issue via wisp_dependencies
	wisp := &types.Issue{
		ID:        "test-wisp-abc",
		Title:     "Test wisp",
		Status:    types.StatusOpen,
		Priority:  2,
		IssueType: types.TypeTask,
		Ephemeral: true,
	}
	if err := store.createWisp(ctx, wisp, "test"); err != nil {
		t.Fatalf("failed to create wisp: %v", err)
	}

	// Add wisp dependency: wisp depends on the permanent issue
	dep := &types.Dependency{
		IssueID:     wisp.ID,
		DependsOnID: "test-old1",
		Type:        types.DepBlocks,
	}
	if err := store.addWispDependency(ctx, dep, "test"); err != nil {
		t.Fatalf("failed to add wisp dependency: %v", err)
	}

	// Add wisp dependency: some other wisp has issue_id = old issue
	wisp2 := &types.Issue{
		ID:        "test-wisp-def",
		Title:     "Another wisp",
		Status:    types.StatusOpen,
		Priority:  2,
		IssueType: types.TypeTask,
		Ephemeral: true,
	}
	if err := store.createWisp(ctx, wisp2, "test"); err != nil {
		t.Fatalf("failed to create wisp2: %v", err)
	}

	// Add wisp_dependencies row where issue_id is the old ID
	dep2 := &types.Dependency{
		IssueID:     "test-old1",
		DependsOnID: wisp2.ID,
		Type:        types.DepBlocks,
	}
	if err := store.addWispDependency(ctx, dep2, "test"); err != nil {
		t.Fatalf("failed to add wisp dependency 2: %v", err)
	}

	// Add a wisp label for the old issue ID
	if err := store.addWispLabel(ctx, "test-old1", "bug", "test"); err != nil {
		t.Fatalf("failed to add wisp label: %v", err)
	}

	// Add a wisp event for the old issue ID (via direct SQL since there's no addWispEvent)
	_, err := store.execContext(ctx, `
		INSERT INTO wisp_events (issue_id, event_type, actor) VALUES (?, 'test_event', 'test')
	`, "test-old1")
	if err != nil {
		t.Fatalf("failed to add wisp event: %v", err)
	}

	// Add a wisp comment for the old issue ID
	_, err = store.execContext(ctx, `
		INSERT INTO wisp_comments (issue_id, author, text) VALUES (?, 'test', 'test comment')
	`, "test-old1")
	if err != nil {
		t.Fatalf("failed to add wisp comment: %v", err)
	}

	// Now rename the issue
	newID := "test-new1"
	if err := store.UpdateIssueID(ctx, "test-old1", newID, issue, "test"); err != nil {
		t.Fatalf("UpdateIssueID failed: %v", err)
	}

	// Verify wisp_dependencies.depends_on_id was updated
	var depCount int
	err = store.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM wisp_dependencies WHERE depends_on_id = ?`, newID).Scan(&depCount)
	if err != nil {
		t.Fatalf("failed to query wisp_dependencies depends_on_id: %v", err)
	}
	if depCount != 1 {
		t.Errorf("expected 1 wisp_dependencies row with depends_on_id=%q, got %d", newID, depCount)
	}

	// Verify wisp_dependencies.issue_id was updated
	err = store.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM wisp_dependencies WHERE issue_id = ?`, newID).Scan(&depCount)
	if err != nil {
		t.Fatalf("failed to query wisp_dependencies issue_id: %v", err)
	}
	if depCount != 1 {
		t.Errorf("expected 1 wisp_dependencies row with issue_id=%q, got %d", newID, depCount)
	}

	// Verify old ID is gone from wisp_dependencies
	var oldCount int
	err = store.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM wisp_dependencies WHERE issue_id = ? OR depends_on_id = ?`,
		"test-old1", "test-old1").Scan(&oldCount)
	if err != nil {
		t.Fatalf("failed to query old wisp_dependencies: %v", err)
	}
	if oldCount != 0 {
		t.Errorf("expected 0 wisp_dependencies rows with old ID, got %d", oldCount)
	}

	// Verify wisp_events was updated
	var eventCount int
	err = store.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM wisp_events WHERE issue_id = ?`, newID).Scan(&eventCount)
	if err != nil {
		t.Fatalf("failed to query wisp_events: %v", err)
	}
	if eventCount != 1 {
		t.Errorf("expected 1 wisp_events row with issue_id=%q, got %d", newID, eventCount)
	}

	// Verify wisp_labels was updated
	var labelCount int
	err = store.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM wisp_labels WHERE issue_id = ?`, newID).Scan(&labelCount)
	if err != nil {
		t.Fatalf("failed to query wisp_labels: %v", err)
	}
	if labelCount != 1 {
		t.Errorf("expected 1 wisp_labels row with issue_id=%q, got %d", newID, labelCount)
	}

	// Verify wisp_comments was updated
	var commentCount int
	err = store.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM wisp_comments WHERE issue_id = ?`, newID).Scan(&commentCount)
	if err != nil {
		t.Fatalf("failed to query wisp_comments: %v", err)
	}
	if commentCount != 1 {
		t.Errorf("expected 1 wisp_comments row with issue_id=%q, got %d", newID, commentCount)
	}
}
