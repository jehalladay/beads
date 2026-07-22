package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/types"
)

// beads-wart9: DeleteIssuesBySourceRepoInTx (internal/storage/issueops/bulk_ops.go)
// is the 6th bulk-delete path — the one rb00b (3cc7302ab) never touched. It ran a
// raw `DELETE FROM issues WHERE source_repo = ?` with no tombstone rewrite, so a
// survivor that referenced (in prose) an issue deleted by `bd repo remove` kept the
// raw dangling id, defeating the [deleted:id] tombstone convention rb00b established
// for the other five delete paths.
//
// This is the store-level analogue of purge_tombstone_rb00b_test.go / gc_decay_
// tombstone_rb00b_test.go: a survivor referencing a to-be-deleted issue must have
// its reference rewritten to [deleted:<id>] once the referenced issue is removed by
// source-repo delete. Because both DoltStore.DeleteIssuesBySourceRepo and
// EmbeddedDoltStore.DeleteIssuesBySourceRepo route through the single
// issueops.DeleteIssuesBySourceRepoInTx, this one fix covers both backends.
func TestDeleteIssuesBySourceRepoTombstonesTextRefs_wart9(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	testDB := filepath.Join(tmpDir, ".beads", "beads.db")
	s := newTestStore(t, testDB)
	ctx := context.Background()

	const doomedRepo = "/path/to/doomed/repo"

	// The issue that will be deleted by `bd repo remove <doomedRepo>`.
	doomed := &types.Issue{
		Title:      "Doomed issue",
		Priority:   1,
		Status:     types.StatusOpen,
		IssueType:  types.TypeTask,
		SourceRepo: doomedRepo,
		CreatedAt:  time.Now(),
	}
	if err := s.CreateIssue(ctx, doomed, "test"); err != nil {
		t.Fatalf("failed to create doomed issue: %v", err)
	}

	// A durable survivor in a DIFFERENT repo that references the doomed issue in
	// its description. It must survive the source-repo delete AND have its live
	// reference tombstoned.
	survivor := &types.Issue{
		Title:       "Survivor issue",
		Priority:    1,
		Status:      types.StatusOpen,
		IssueType:   types.TypeTask,
		SourceRepo:  "/path/to/other/repo",
		Description: "depends on the work in " + doomed.ID,
		CreatedAt:   time.Now(),
	}
	if err := s.CreateIssue(ctx, survivor, "test"); err != nil {
		t.Fatalf("failed to create survivor issue: %v", err)
	}

	// The tombstone rewriter only visits issues CONNECTED to the deleted set via a
	// dependency edge (in either direction), matching rb00b's neighbor traversal.
	dep := &types.Dependency{
		IssueID:     survivor.ID,
		DependsOnID: doomed.ID,
		Type:        types.DepBlocks,
		CreatedAt:   time.Now(),
	}
	if err := s.AddDependency(ctx, dep, "test"); err != nil {
		t.Fatalf("failed to add dependency: %v", err)
	}

	// Delete the doomed repo's issues (the `bd repo remove` code path).
	deleted, err := s.DeleteIssuesBySourceRepo(ctx, doomedRepo)
	if err != nil {
		t.Fatalf("DeleteIssuesBySourceRepo: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("DeleteIssuesBySourceRepo deleted %d issues, want 1 (the doomed issue)", deleted)
	}

	// The survivor must remain AND its live reference must now be tombstoned.
	got, err := s.GetIssue(ctx, survivor.ID)
	if err != nil {
		t.Fatalf("survivor issue missing after source-repo delete: %v", err)
	}
	wantTomb := "[deleted:" + doomed.ID + "]"
	if !strings.Contains(got.Description, wantTomb) {
		t.Errorf("source-repo delete left a dangling text ref: survivor description = %q, want it to contain %q",
			got.Description, wantTomb)
	}
	// And the raw live id must no longer stand alone (the rewrite replaced it).
	if strings.Contains(got.Description, "in "+doomed.ID) {
		t.Errorf("survivor description still holds the raw live ref %q: %q", doomed.ID, got.Description)
	}
}
