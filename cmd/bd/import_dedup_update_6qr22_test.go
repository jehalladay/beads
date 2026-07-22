package main

import (
	"context"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestFilterDuplicatesByTitle_UpdateNotDropped_6qr22 covers beads-6qr22:
// `bd import --dedup` silently DROPPED a genuine update to an EXISTING issue
// when the incoming row kept its title. The dedup title-set was seeded from
// all open issues INCLUDING the incoming row's own existing issue, and the
// filter matched on title alone with no exclusion of the incoming row's own
// id — so an id-carrying update row title-matched ITSELF and was skipped as a
// "duplicate", losing its (strictly-newer) field changes.
//
// The fix excludes the incoming batch's own ids when seeding the pre-existing
// title set: an id that already exists locally is an UPDATE (upsert) target,
// not a new duplicate. A NEW-id row whose title matches an existing issue is
// still a genuine fresh duplicate and stays skipped.
func TestFilterDuplicatesByTitle_UpdateNotDropped_6qr22(t *testing.T) {
	t.Parallel()

	mk := func(id, title, status string) *types.Issue {
		return &types.Issue{ID: id, Title: title, Status: types.Status(status)}
	}

	t.Run("same-id row (an update) is NOT dropped even though its title matches its own existing issue", func(t *testing.T) {
		t.Parallel()
		// The existing issue and the incoming row share the same id AND title:
		// the incoming row is an update of that very issue.
		st := &fakeDedupStore{existing: []*types.Issue{mk("bd-1", "unique task", "open")}}
		in := []*types.Issue{mk("bd-1", "unique task", "open")}
		kept, skipped := filterDuplicatesByTitle(context.Background(), st, in)
		if skipped != 0 {
			t.Errorf("an id-carrying update must not be dedup-skipped: skipped=%d, want 0", skipped)
		}
		if len(kept) != 1 || kept[0].ID != "bd-1" {
			t.Fatalf("the update row must be kept for the upsert: kept=%v", kept)
		}
	})

	t.Run("a NEW-id row whose title matches an existing OPEN issue is still skipped", func(t *testing.T) {
		t.Parallel()
		// A fresh row (different id) that duplicates an existing open title is a
		// genuine dup and must still be skipped — the fix must not over-broaden.
		st := &fakeDedupStore{existing: []*types.Issue{mk("bd-1", "shared title", "open")}}
		in := []*types.Issue{
			mk("bd-2", "shared title", "open"), // new id, dup title → skip
			mk("bd-3", "brand new", "open"),    // keep
		}
		kept, skipped := filterDuplicatesByTitle(context.Background(), st, in)
		if skipped != 1 {
			t.Errorf("a fresh title-dup (new id) must still be skipped: skipped=%d, want 1", skipped)
		}
		if len(kept) != 1 || kept[0].ID != "bd-3" {
			t.Fatalf("only the genuinely new row should be kept: kept=%v", kept)
		}
	})

	t.Run("update row present alongside a fresh dup: update kept, fresh dup skipped", func(t *testing.T) {
		t.Parallel()
		// Both an update (bd-1, matches existing bd-1) and a fresh dup (bd-9,
		// new id but same title as the update) arrive. The update is kept; the
		// fresh dup title-matches the kept update and is skipped (first-wins,
		// 0bvog intra-batch behavior preserved).
		st := &fakeDedupStore{existing: []*types.Issue{mk("bd-1", "recurring title", "open")}}
		in := []*types.Issue{
			mk("bd-1", "recurring title", "open"), // update target → keep
			mk("bd-9", "recurring title", "open"), // fresh dup of the kept title → skip
		}
		kept, skipped := filterDuplicatesByTitle(context.Background(), st, in)
		if skipped != 1 {
			t.Errorf("fresh dup of a kept update title should be skipped: skipped=%d, want 1", skipped)
		}
		if len(kept) != 1 || kept[0].ID != "bd-1" {
			t.Fatalf("the update must survive, the fresh dup drop: kept=%v", kept)
		}
	})
}
