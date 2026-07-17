package domain

import (
	"context"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// reparent94iaFake is a minimal DependencySQLRepository for the reparent
// multi-parent regression (beads-94ia). It embeds the interface (so it
// satisfies all 18 methods without stubbing each) and overrides only the three
// reparent() actually calls: ListByIssueIDs, Delete, Insert. A distinct type
// name avoids colliding with sibling coverage fakes (e.g. beads-5af2's
// fakeDepRepo) when both land through the refinery.
type reparent94iaFake struct {
	DependencySQLRepository // embedded; unused methods panic if ever called

	parents  []string // current parent-child DependsOnIDs for the child
	deleted  []string // parents removed via Delete
	inserted []string // parents added via Insert
}

func (f *reparent94iaFake) ListByIssueIDs(ctx context.Context, issueIDs []string, opts DepListOpts) (DepBulkResult, error) {
	deps := make([]*types.Dependency, 0, len(f.parents))
	for _, p := range f.parents {
		deps = append(deps, &types.Dependency{
			IssueID:     issueIDs[0],
			DependsOnID: p,
			Type:        types.DepParentChild,
		})
	}
	return DepBulkResult{Outgoing: map[string][]*types.Dependency{issueIDs[0]: deps}}, nil
}

func (f *reparent94iaFake) Delete(ctx context.Context, issueID, dependsOnID, actor string, opts DepInsertOpts) (DepDeleteResult, error) {
	f.deleted = append(f.deleted, dependsOnID)
	return DepDeleteResult{}, nil
}

func (f *reparent94iaFake) Insert(ctx context.Context, dep *types.Dependency, actor string, opts DepInsertOpts) error {
	f.inserted = append(f.inserted, dep.DependsOnID)
	return nil
}

// TestReparent_RemovesAllOldParents is the beads-94ia regression: a child with
// MORE THAN ONE parent-child edge (created via `bd dep add ... --type
// parent-child`, which has no single-parent guard) must have ALL prior parent
// edges removed on reparent, not just the first. The old code took the first
// edge and `break`ed, silently leaving stale parents and corrupting the tree.
func TestReparent_RemovesAllOldParents(t *testing.T) {
	ctx := context.Background()

	fake := &reparent94iaFake{parents: []string{"oldA", "oldB", "oldC"}}
	uc := NewDependencyUseCase(fake)

	if err := uc.Reparent(ctx, "child", "newP", "actor"); err != nil {
		t.Fatalf("Reparent: %v", err)
	}

	// All three old parents must be deleted (the bug deleted only "oldA").
	if len(fake.deleted) != 3 {
		t.Fatalf("deleted %v, want all 3 old parents [oldA oldB oldC]", fake.deleted)
	}
	wantDeleted := map[string]bool{"oldA": true, "oldB": true, "oldC": true}
	for _, d := range fake.deleted {
		if !wantDeleted[d] {
			t.Errorf("unexpected delete %q", d)
		}
	}
	// The new parent is inserted exactly once.
	if len(fake.inserted) != 1 || fake.inserted[0] != "newP" {
		t.Errorf("inserted %v, want [newP]", fake.inserted)
	}
}

// TestReparent_NewParentAmongExisting covers the case where the desired new
// parent is already one of several parent-child edges: the OTHER stale parents
// are removed and the new parent is NOT re-inserted (no duplicate edge).
func TestReparent_NewParentAmongExisting(t *testing.T) {
	ctx := context.Background()

	fake := &reparent94iaFake{parents: []string{"oldA", "newP", "oldB"}}
	uc := NewDependencyUseCase(fake)

	if err := uc.Reparent(ctx, "child", "newP", "actor"); err != nil {
		t.Fatalf("Reparent: %v", err)
	}

	// oldA and oldB removed; newP kept (not deleted, not re-inserted).
	if len(fake.deleted) != 2 {
		t.Fatalf("deleted %v, want [oldA oldB]", fake.deleted)
	}
	for _, d := range fake.deleted {
		if d == "newP" {
			t.Errorf("newP was deleted but should be kept")
		}
	}
	if len(fake.inserted) != 0 {
		t.Errorf("inserted %v, want none (newP already set)", fake.inserted)
	}
}

// TestReparent_OnlyDesiredParent is the fast-path no-op: the sole parent-child
// edge is already the desired one, so nothing is touched.
func TestReparent_OnlyDesiredParent(t *testing.T) {
	ctx := context.Background()

	fake := &reparent94iaFake{parents: []string{"newP"}}
	uc := NewDependencyUseCase(fake)

	if err := uc.Reparent(ctx, "child", "newP", "actor"); err != nil {
		t.Fatalf("Reparent: %v", err)
	}
	if len(fake.deleted) != 0 || len(fake.inserted) != 0 {
		t.Errorf("no-op reparent touched repo: del=%v ins=%v", fake.deleted, fake.inserted)
	}
}
