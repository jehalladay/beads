package sqlite

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/types"
)

func TestGetTier1Candidates(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	// Create test issues
	// Old closed issue (eligible)
	issue1 := &types.Issue{
		ID:          "bd-1",
		Title:       "Old closed issue",
		Description: "This is a test description",
		Status:      "closed",
		Priority:    2,
		IssueType:   "task",
		ClosedAt:    timePtr(time.Now().Add(-40 * 24 * time.Hour)),
	}
	if err := store.CreateIssue(ctx, issue1, "test"); err != nil {
		t.Fatalf("Failed to create issue1: %v", err)
	}

	// Recently closed issue (not eligible - too recent)
	issue2 := &types.Issue{
		ID:          "bd-2",
		Title:       "Recent closed issue",
		Description: "Recent",
		Status:      "closed",
		Priority:    2,
		IssueType:   "task",
		ClosedAt:    timePtr(time.Now().Add(-10 * 24 * time.Hour)),
	}
	if err := store.CreateIssue(ctx, issue2, "test"); err != nil {
		t.Fatalf("Failed to create issue2: %v", err)
	}

	// Open issue (not eligible)
	issue3 := &types.Issue{
		ID:          "bd-3",
		Title:       "Open issue",
		Description: "Open",
		Status:      "open",
		Priority:    2,
		IssueType:   "task",
	}
	if err := store.CreateIssue(ctx, issue3, "test"); err != nil {
		t.Fatalf("Failed to create issue3: %v", err)
	}

	// Old closed issue with open dependent (not eligible)
	issue4 := &types.Issue{
		ID:          "bd-4",
		Title:       "Has open dependent",
		Description: "Blocked by open issue",
		Status:      "closed",
		Priority:    2,
		IssueType:   "task",
		ClosedAt:    timePtr(time.Now().Add(-40 * 24 * time.Hour)),
	}
	if err := store.CreateIssue(ctx, issue4, "test"); err != nil {
		t.Fatalf("Failed to create issue4: %v", err)
	}

	// Create blocking dependency
	dep := &types.Dependency{
		IssueID:     "bd-3",
		DependsOnID: "bd-4",
		Type:        "blocks",
	}
	if err := store.AddDependency(ctx, dep, "test"); err != nil {
		t.Fatalf("Failed to add dependency: %v", err)
	}

	// Get candidates
	candidates, err := store.GetTier1Candidates(ctx)
	if err != nil {
		t.Fatalf("GetTier1Candidates failed: %v", err)
	}

	// Should only return bd-1 (old and no open dependents)
	if len(candidates) != 1 {
		t.Errorf("Expected 1 candidate, got %d", len(candidates))
	}

	if len(candidates) > 0 && candidates[0].IssueID != "bd-1" {
		t.Errorf("Expected candidate bd-1, got %s", candidates[0].IssueID)
	}
}

func TestGetTier2Candidates(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	// Create old tier1 compacted issue with many events
	issue1 := &types.Issue{
		ID:          "bd-1",
		Title:       "Tier1 compacted with events",
		Description: "Summary",
		Status:      "closed",
		Priority:    2,
		IssueType:   "task",
		ClosedAt:    timePtr(time.Now().Add(-100 * 24 * time.Hour)),
	}
	if err := store.CreateIssue(ctx, issue1, "test"); err != nil {
		t.Fatalf("Failed to create issue1: %v", err)
	}

	// Set compaction level to 1
	_, err := store.db.ExecContext(ctx, `
		UPDATE issues 
		SET compaction_level = 1, 
		    compacted_at = datetime('now', '-95 days'),
		    original_size = 1000
		WHERE id = ?
	`, "bd-1")
	if err != nil {
		t.Fatalf("Failed to set compaction level: %v", err)
	}

	// Add many events (simulate high activity)
	for i := 0; i < 120; i++ {
		if err := store.AddComment(ctx, "bd-1", "test", "comment"); err != nil {
			t.Fatalf("Failed to add event: %v", err)
		}
	}

	// Get tier2 candidates
	candidates, err := store.GetTier2Candidates(ctx)
	if err != nil {
		t.Fatalf("GetTier2Candidates failed: %v", err)
	}

	// Should return bd-1
	if len(candidates) != 1 {
		t.Errorf("Expected 1 candidate, got %d", len(candidates))
	}

	if len(candidates) > 0 && candidates[0].IssueID != "bd-1" {
		t.Errorf("Expected candidate bd-1, got %s", candidates[0].IssueID)
	}
}

func TestCheckEligibilityTier1(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	// Create eligible issue
	issue1 := &types.Issue{
		ID:          "bd-1",
		Title:       "Eligible",
		Description: "Test",
		Status:      "closed",
		Priority:    2,
		IssueType:   "task",
		ClosedAt:    timePtr(time.Now().Add(-40 * 24 * time.Hour)),
	}
	if err := store.CreateIssue(ctx, issue1, "test"); err != nil {
		t.Fatalf("Failed to create issue: %v", err)
	}

	eligible, reason, err := store.CheckEligibility(ctx, "bd-1", 1)
	if err != nil {
		t.Fatalf("CheckEligibility failed: %v", err)
	}

	if !eligible {
		t.Errorf("Expected eligible, got not eligible: %s", reason)
	}
}

func TestCheckEligibilityOpenIssue(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	issue := &types.Issue{
		ID:          "bd-1",
		Title:       "Open",
		Description: "Test",
		Status:      "open",
		Priority:    2,
		IssueType:   "task",
	}
	if err := store.CreateIssue(ctx, issue, "test"); err != nil {
		t.Fatalf("Failed to create issue: %v", err)
	}

	eligible, reason, err := store.CheckEligibility(ctx, "bd-1", 1)
	if err != nil {
		t.Fatalf("CheckEligibility failed: %v", err)
	}

	if eligible {
		t.Error("Expected not eligible for open issue")
	}

	if reason != "issue is not closed" {
		t.Errorf("Expected 'issue is not closed', got '%s'", reason)
	}
}

func TestCheckEligibilityAlreadyCompacted(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	issue := &types.Issue{
		ID:          "bd-1",
		Title:       "Already compacted",
		Description: "Test",
		Status:      "closed",
		Priority:    2,
		IssueType:   "task",
		ClosedAt:    timePtr(time.Now().Add(-40 * 24 * time.Hour)),
	}
	if err := store.CreateIssue(ctx, issue, "test"); err != nil {
		t.Fatalf("Failed to create issue: %v", err)
	}

	// Mark as compacted
	_, err := store.db.ExecContext(ctx, `
		UPDATE issues SET compaction_level = 1 WHERE id = ?
	`, "bd-1")
	if err != nil {
		t.Fatalf("Failed to set compaction level: %v", err)
	}

	eligible, reason, err := store.CheckEligibility(ctx, "bd-1", 1)
	if err != nil {
		t.Fatalf("CheckEligibility failed: %v", err)
	}

	if eligible {
		t.Error("Expected not eligible for already compacted issue")
	}

	if reason != "issue is already compacted" {
		t.Errorf("Expected 'issue is already compacted', got '%s'", reason)
	}
}

func TestTier1NoCircularDeps(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	// Create three closed issues with circular dependency
	issue1 := &types.Issue{
		ID:          "bd-1",
		Title:       "Issue 1",
		Description: "Test",
		Status:      "closed",
		Priority:    2,
		IssueType:   "task",
		ClosedAt:    timePtr(time.Now().Add(-40 * 24 * time.Hour)),
	}
	issue2 := &types.Issue{
		ID:          "bd-2",
		Title:       "Issue 2",
		Description: "Test",
		Status:      "closed",
		Priority:    2,
		IssueType:   "task",
		ClosedAt:    timePtr(time.Now().Add(-40 * 24 * time.Hour)),
	}
	issue3 := &types.Issue{
		ID:          "bd-3",
		Title:       "Issue 3",
		Description: "Test",
		Status:      "closed",
		Priority:    2,
		IssueType:   "task",
		ClosedAt:    timePtr(time.Now().Add(-40 * 24 * time.Hour)),
	}

	for _, issue := range []*types.Issue{issue1, issue2, issue3} {
		if err := store.CreateIssue(ctx, issue, "test"); err != nil {
			t.Fatalf("Failed to create issue: %v", err)
		}
	}

	// Create circular dependency: 1->2->3->1
	// Note: the AddDependency validation should prevent this, but let's test the query handles it
	_, err := store.db.ExecContext(ctx, `
		INSERT INTO dependencies (issue_id, depends_on_id, type, created_by) VALUES
			('bd-1', 'bd-2', 'blocks', 'test'),
			('bd-2', 'bd-3', 'blocks', 'test'),
			('bd-3', 'bd-1', 'blocks', 'test')
	`)
	if err != nil {
		t.Fatalf("Failed to create dependencies: %v", err)
	}

	// Should not crash and should return all three as they're all closed
	candidates, err := store.GetTier1Candidates(ctx)
	if err != nil {
		t.Fatalf("GetTier1Candidates failed with circular deps: %v", err)
	}

	// All should be eligible since all are closed
	if len(candidates) != 3 {
		t.Errorf("Expected 3 candidates, got %d", len(candidates))
	}
}

func TestCreateSnapshot(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	issue := &types.Issue{
		ID:                 "bd-1",
		Title:              "Test Issue",
		Description:        "Original description",
		Design:             "Design notes",
		Notes:              "Additional notes",
		AcceptanceCriteria: "Must work",
		Status:             "closed",
		Priority:           2,
		IssueType:          "task",
		ClosedAt:           timePtr(time.Now()),
	}
	if err := store.CreateIssue(ctx, issue, "test"); err != nil {
		t.Fatalf("Failed to create issue: %v", err)
	}

	err := store.CreateSnapshot(ctx, issue, 1)
	if err != nil {
		t.Fatalf("CreateSnapshot failed: %v", err)
	}

	snapshots, err := store.GetSnapshots(ctx, issue.ID)
	if err != nil {
		t.Fatalf("GetSnapshots failed: %v", err)
	}

	if len(snapshots) != 1 {
		t.Fatalf("Expected 1 snapshot, got %d", len(snapshots))
	}

	snapshot := snapshots[0]
	if snapshot.Description != issue.Description {
		t.Errorf("Expected description %q, got %q", issue.Description, snapshot.Description)
	}
	if snapshot.Design != issue.Design {
		t.Errorf("Expected design %q, got %q", issue.Design, snapshot.Design)
	}
	if snapshot.Notes != issue.Notes {
		t.Errorf("Expected notes %q, got %q", issue.Notes, snapshot.Notes)
	}
	if snapshot.AcceptanceCriteria != issue.AcceptanceCriteria {
		t.Errorf("Expected criteria %q, got %q", issue.AcceptanceCriteria, snapshot.AcceptanceCriteria)
	}
}

func TestCreateSnapshotUTF8(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	issue := &types.Issue{
		ID:                 "bd-1",
		Title:              "UTF-8 Test 🎉",
		Description:        "Café, résumé, 日本語, emoji 🚀",
		Design:             "Design with 中文 and émojis 🔥",
		Notes:              "Notes: ñ, ü, é, à",
		AcceptanceCriteria: "Must handle UTF-8 correctly ✅",
		Status:             "closed",
		Priority:           2,
		IssueType:          "task",
		ClosedAt:           timePtr(time.Now()),
	}
	if err := store.CreateIssue(ctx, issue, "test"); err != nil {
		t.Fatalf("Failed to create issue: %v", err)
	}

	err := store.CreateSnapshot(ctx, issue, 1)
	if err != nil {
		t.Fatalf("CreateSnapshot failed: %v", err)
	}

	snapshots, err := store.GetSnapshots(ctx, issue.ID)
	if err != nil {
		t.Fatalf("GetSnapshots failed: %v", err)
	}

	if len(snapshots) != 1 {
		t.Fatalf("Expected 1 snapshot, got %d", len(snapshots))
	}

	snapshot := snapshots[0]
	if snapshot.Title != issue.Title {
		t.Errorf("UTF-8 title not preserved: expected %q, got %q", issue.Title, snapshot.Title)
	}
	if snapshot.Description != issue.Description {
		t.Errorf("UTF-8 description not preserved: expected %q, got %q", issue.Description, snapshot.Description)
	}
	if snapshot.Design != issue.Design {
		t.Errorf("UTF-8 design not preserved: expected %q, got %q", issue.Design, snapshot.Design)
	}
}

func TestCreateMultipleSnapshots(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	issue := &types.Issue{
		ID:          "bd-1",
		Title:       "Test Issue",
		Description: "Original",
		Status:      "closed",
		Priority:    2,
		IssueType:   "task",
		ClosedAt:    timePtr(time.Now()),
	}
	if err := store.CreateIssue(ctx, issue, "test"); err != nil {
		t.Fatalf("Failed to create issue: %v", err)
	}

	if err := store.CreateSnapshot(ctx, issue, 1); err != nil {
		t.Fatalf("CreateSnapshot level 1 failed: %v", err)
	}

	issue.Description = "Compacted once"
	if err := store.CreateSnapshot(ctx, issue, 2); err != nil {
		t.Fatalf("CreateSnapshot level 2 failed: %v", err)
	}

	snapshots, err := store.GetSnapshots(ctx, issue.ID)
	if err != nil {
		t.Fatalf("GetSnapshots failed: %v", err)
	}

	if len(snapshots) != 2 {
		t.Fatalf("Expected 2 snapshots, got %d", len(snapshots))
	}

	if snapshots[0].CompactionLevel != 1 {
		t.Errorf("Expected first snapshot level 1, got %d", snapshots[0].CompactionLevel)
	}
	if snapshots[1].CompactionLevel != 2 {
		t.Errorf("Expected second snapshot level 2, got %d", snapshots[1].CompactionLevel)
	}
}

func TestRestoreFromSnapshot(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	issue := &types.Issue{
		ID:                 "bd-1",
		Title:              "Original Title",
		Description:        "Original description",
		Design:             "Original design",
		Notes:              "Original notes",
		AcceptanceCriteria: "Original criteria",
		Status:             "closed",
		Priority:           2,
		IssueType:          "task",
		ClosedAt:           timePtr(time.Now()),
	}
	if err := store.CreateIssue(ctx, issue, "test"); err != nil {
		t.Fatalf("Failed to create issue: %v", err)
	}

	if err := store.CreateSnapshot(ctx, issue, 1); err != nil {
		t.Fatalf("CreateSnapshot failed: %v", err)
	}

	_, err := store.db.ExecContext(ctx, `
		UPDATE issues 
		SET description = 'Compacted', 
		    design = '', 
		    notes = '', 
		    acceptance_criteria = '',
		    compaction_level = 1
		WHERE id = ?
	`, issue.ID)
	if err != nil {
		t.Fatalf("Failed to update issue: %v", err)
	}

	err = store.RestoreFromSnapshot(ctx, issue.ID, 1)
	if err != nil {
		t.Fatalf("RestoreFromSnapshot failed: %v", err)
	}

	restored, err := store.GetIssue(ctx, issue.ID)
	if err != nil {
		t.Fatalf("GetIssue failed: %v", err)
	}

	if restored.Description != issue.Description {
		t.Errorf("Description not restored: expected %q, got %q", issue.Description, restored.Description)
	}
	if restored.Design != issue.Design {
		t.Errorf("Design not restored: expected %q, got %q", issue.Design, restored.Design)
	}
	if restored.Notes != issue.Notes {
		t.Errorf("Notes not restored: expected %q, got %q", issue.Notes, restored.Notes)
	}
	if restored.AcceptanceCriteria != issue.AcceptanceCriteria {
		t.Errorf("Criteria not restored: expected %q, got %q", issue.AcceptanceCriteria, restored.AcceptanceCriteria)
	}
}

func TestRestoreSnapshotNoSnapshot(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	issue := &types.Issue{
		ID:          "bd-1",
		Title:       "Test",
		Description: "Test",
		Status:      "closed",
		Priority:    2,
		IssueType:   "task",
		ClosedAt:    timePtr(time.Now()),
	}
	if err := store.CreateIssue(ctx, issue, "test"); err != nil {
		t.Fatalf("Failed to create issue: %v", err)
	}

	err := store.RestoreFromSnapshot(ctx, issue.ID, 1)
	if err == nil {
		t.Fatal("Expected error when no snapshot exists")
	}
	if !strings.Contains(err.Error(), "no snapshot found") {
		t.Errorf("Expected 'no snapshot found' error, got: %v", err)
	}
}

func TestApplyCompaction(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	issue := &types.Issue{
		ID:          "bd-1",
		Title:       "Test",
		Description: "Original description that is quite long",
		Status:      "closed",
		Priority:    2,
		IssueType:   "task",
		ClosedAt:    timePtr(time.Now()),
	}
	if err := store.CreateIssue(ctx, issue, "test"); err != nil {
		t.Fatalf("Failed to create issue: %v", err)
	}

	originalSize := len(issue.Description)
	err := store.ApplyCompaction(ctx, issue.ID, 1, originalSize)
	if err != nil {
		t.Fatalf("ApplyCompaction failed: %v", err)
	}

	var compactionLevel int
	var compactedAt sql.NullTime
	var storedSize int
	err = store.db.QueryRowContext(ctx, `
		SELECT COALESCE(compaction_level, 0), compacted_at, COALESCE(original_size, 0)
		FROM issues WHERE id = ?
	`, issue.ID).Scan(&compactionLevel, &compactedAt, &storedSize)
	if err != nil {
		t.Fatalf("Failed to query issue: %v", err)
	}

	if compactionLevel != 1 {
		t.Errorf("Expected compaction_level 1, got %d", compactionLevel)
	}
	if !compactedAt.Valid {
		t.Error("Expected compacted_at to be set")
	}
	if storedSize != originalSize {
		t.Errorf("Expected original_size %d, got %d", originalSize, storedSize)
	}
}

func timePtr(t time.Time) *time.Time {
	return &t
}
