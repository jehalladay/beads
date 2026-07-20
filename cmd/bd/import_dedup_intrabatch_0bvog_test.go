package main

import (
	"context"
	"testing"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
)

// fakeDedupStore returns a fixed existing-issue set from SearchIssues so
// filterDuplicatesByTitle can be unit-tested without a real DB.
type fakeDedupStore struct {
	storage.DoltStorage
	existing []*types.Issue
}

func (f *fakeDedupStore) SearchIssues(_ context.Context, _ string, _ types.IssueFilter) ([]*types.Issue, error) {
	return f.existing, nil
}

// TestFilterDuplicatesByTitle_IntraBatch_0bvog covers beads-0bvog: --dedup must
// skip a title that duplicates ANOTHER row in the SAME import batch, not only
// titles that match a pre-existing issue. The old code seeded titleSet from
// existing issues only and never folded a kept row's title back in, so two
// import rows with an identical new title both survived.
func TestFilterDuplicatesByTitle_IntraBatch_0bvog(t *testing.T) {
	t.Parallel()

	mk := func(id, title, status string) *types.Issue {
		return &types.Issue{ID: id, Title: title, Status: types.Status(status)}
	}

	t.Run("two new rows with the same title -> one skipped (intra-batch)", func(t *testing.T) {
		t.Parallel()
		st := &fakeDedupStore{existing: nil} // no pre-existing issues
		in := []*types.Issue{
			mk("t-1", "Duplicate title", "open"),
			mk("t-2", "Duplicate title", "open"),
			mk("t-3", "Unique title", "open"),
		}
		kept, skipped := filterDuplicatesByTitle(context.Background(), st, in)
		if skipped != 1 {
			t.Errorf("intra-batch dup: skipped=%d, want 1", skipped)
		}
		if len(kept) != 2 {
			t.Fatalf("intra-batch dup: kept=%d, want 2 (first Duplicate + Unique)", len(kept))
		}
		// First-wins: the first "Duplicate title" is kept, the second dropped.
		if kept[0].ID != "t-1" || kept[1].ID != "t-3" {
			t.Errorf("intra-batch dup: kept IDs=%v, want [t-1 t-3] (first-wins)", []string{kept[0].ID, kept[1].ID})
		}
	})

	t.Run("case-insensitive intra-batch dup", func(t *testing.T) {
		t.Parallel()
		st := &fakeDedupStore{existing: nil}
		in := []*types.Issue{
			mk("t-1", "Same Title", "open"),
			mk("t-2", "same title", "open"),
		}
		kept, skipped := filterDuplicatesByTitle(context.Background(), st, in)
		if skipped != 1 || len(kept) != 1 {
			t.Errorf("case-insensitive intra-batch: kept=%d skipped=%d, want 1/1", len(kept), skipped)
		}
	})

	t.Run("still skips titles matching an existing OPEN issue", func(t *testing.T) {
		t.Parallel()
		st := &fakeDedupStore{existing: []*types.Issue{mk("x-9", "Existing open", "open")}}
		in := []*types.Issue{
			mk("t-1", "Existing open", "open"), // matches existing → skip
			mk("t-2", "Brand new", "open"),     // keep
		}
		kept, skipped := filterDuplicatesByTitle(context.Background(), st, in)
		if skipped != 1 || len(kept) != 1 || kept[0].ID != "t-2" {
			t.Errorf("existing-match dedup regressed: kept=%v skipped=%d", kept, skipped)
		}
	})

	t.Run("a title matching an existing CLOSED issue is NOT skipped", func(t *testing.T) {
		t.Parallel()
		st := &fakeDedupStore{existing: []*types.Issue{mk("x-9", "Reused title", "closed")}}
		in := []*types.Issue{mk("t-1", "Reused title", "open")}
		kept, skipped := filterDuplicatesByTitle(context.Background(), st, in)
		if skipped != 0 || len(kept) != 1 {
			t.Errorf("closed-existing should NOT block a re-import: kept=%d skipped=%d, want 1/0", len(kept), skipped)
		}
	})
}
