//go:build cgo

package main

import (
	"context"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestPerformMergeAtomic is the atomicity guard for beads-zcq86: bd merge
// (performMerge) must not leave a split state when the per-source link or
// reparent step fails mid-sequence. Before the fix, performMerge ran each
// source's close and its target link as TWO SEPARATE autocommitting store
// calls (store.CloseIssue then store.AddDependency), and the reparent as two
// more (store.RemoveDependency then store.AddDependency). A failure in the
// second call left either:
//   - (B) the source CLOSED with reason "Duplicate of X" but with NO link to X
//     — a closed duplicate with no provenance edge (the inverse of the
//     edge-added-while-open split njnw's LinkAndClose fixed for
//     bd duplicate / bd supersede), or
//   - (A) a child ORPHANED mid-reparent (old parent link removed, new one not
//     added).
//
// Same MULTI-WRITE-ATOMICITY / split-state class as beads-njnw (the fix this
// path was never migrated to), beads-ary2n, beads-ivnqm.
func TestPerformMergeAtomic(t *testing.T) {
	// ── (B) close+link atomicity: the target link fails after the close ──────
	// We force the "related" link to fail deterministically by pre-seeding a
	// DIFFERENT-typed edge for the same (source -> target) pair. performMerge's
	// AddDependency(type="related") then hits the "already exists with type"
	// conflict — AFTER it has closed the source (pre-fix). Post-fix the whole
	// per-source unit is one transaction, so the close rolls back and the
	// source stays OPEN.
	t.Run("link_failure_does_not_close_source_without_link", func(t *testing.T) {
		tmpDir := t.TempDir()
		testStore := newTestStore(t, tmpDir+"/.beads/beads.db")
		ctx := context.Background()

		oldStore, oldRootCtx, oldActor := store, rootCtx, actor
		store = testStore
		rootCtx = ctx
		actor = "test-user"
		defer func() { store, rootCtx, actor = oldStore, oldRootCtx, oldActor }()

		target := &types.Issue{Title: "Target", Status: types.StatusOpen, Priority: 1, IssueType: types.TypeTask}
		source := &types.Issue{Title: "Source", Status: types.StatusOpen, Priority: 1, IssueType: types.TypeTask}
		for _, iss := range []*types.Issue{target, source} {
			if err := testStore.CreateIssue(ctx, iss, "test"); err != nil {
				t.Fatalf("CreateIssue: %v", err)
			}
		}

		// Pre-seed a conflicting-type edge source -> target so the merge's
		// "related" AddDependency will be rejected ("already exists with type").
		conflict := &types.Dependency{IssueID: source.ID, DependsOnID: target.ID, Type: types.DepBlocks}
		if err := testStore.AddDependency(ctx, conflict, "test"); err != nil {
			t.Fatalf("seed conflicting edge: %v", err)
		}

		result := performMerge(target.ID, []string{source.ID})

		// The link step must have failed (recorded an error, not "linked").
		errs := result["errors"].([]string)
		linked := result["linked"].([]string)
		if len(errs) == 0 {
			t.Fatalf("expected a link error from the conflicting-type edge, got none (linked=%v)", linked)
		}
		if len(linked) != 0 {
			t.Errorf("expected 0 linked (link conflicted), got %v", linked)
		}

		// The atomicity invariant: a source whose provenance link could NOT be
		// written must NOT be left closed. Either both apply or neither.
		got, err := testStore.GetIssue(ctx, source.ID)
		if err != nil {
			t.Fatalf("GetIssue(source): %v", err)
		}
		if got.Status == types.StatusClosed {
			t.Errorf("split state: source %s was CLOSED but its target link failed — "+
				"a closed duplicate with no provenance edge (beads-zcq86). "+
				"performMerge must close+link atomically.", source.ID)
		}
	})

	// ── (A) reparent atomicity: the new-parent add fails after the old is
	// removed. Pre-seed a conflicting-type child -> target edge so the reparent
	// AddDependency(parent-child) is rejected AFTER the old child -> source
	// parent-child link is removed (pre-fix) — orphaning the child.
	t.Run("reparent_failure_does_not_orphan_child", func(t *testing.T) {
		tmpDir := t.TempDir()
		testStore := newTestStore(t, tmpDir+"/.beads/beads.db")
		ctx := context.Background()

		oldStore, oldRootCtx, oldActor := store, rootCtx, actor
		store = testStore
		rootCtx = ctx
		actor = "test-user"
		defer func() { store, rootCtx, actor = oldStore, oldRootCtx, oldActor }()

		target := &types.Issue{Title: "Target", Status: types.StatusOpen, Priority: 1, IssueType: types.TypeTask}
		source := &types.Issue{Title: "Source", Status: types.StatusOpen, Priority: 1, IssueType: types.TypeTask}
		child := &types.Issue{Title: "Child", Status: types.StatusOpen, Priority: 1, IssueType: types.TypeTask}
		for _, iss := range []*types.Issue{target, source, child} {
			if err := testStore.CreateIssue(ctx, iss, "test"); err != nil {
				t.Fatalf("CreateIssue: %v", err)
			}
		}

		// child is a parent-child of source (the link performMerge will move).
		if err := testStore.AddDependency(ctx, &types.Dependency{
			IssueID: child.ID, DependsOnID: source.ID, Type: types.DepParentChild,
		}, "test"); err != nil {
			t.Fatalf("seed child->source parent-child: %v", err)
		}
		// Pre-seed a conflicting-type child -> target edge so the reparent's
		// parent-child AddDependency is rejected ("already exists with type").
		if err := testStore.AddDependency(ctx, &types.Dependency{
			IssueID: child.ID, DependsOnID: target.ID, Type: types.DepBlocks,
		}, "test"); err != nil {
			t.Fatalf("seed conflicting child->target edge: %v", err)
		}

		result := performMerge(target.ID, []string{source.ID})

		errs := result["errors"].([]string)
		if len(errs) == 0 {
			t.Fatalf("expected a reparent error from the conflicting-type edge, got none: %+v", result)
		}

		// The atomicity invariant: the child must retain a parent-child edge to
		// SOMEONE (old source or new target) — it must not be orphaned.
		recs, err := testStore.GetDependencyRecords(ctx, child.ID)
		if err != nil {
			t.Fatalf("GetDependencyRecords(child): %v", err)
		}
		hasParent := false
		for _, d := range recs {
			if d.Type == types.DepParentChild &&
				(d.DependsOnID == source.ID || d.DependsOnID == target.ID) {
				hasParent = true
			}
		}
		if !hasParent {
			t.Errorf("split state: child %s was ORPHANED mid-reparent — old parent-child "+
				"link removed but the new one failed (beads-zcq86). performMerge must "+
				"reparent atomically.", child.ID)
		}
	})
}
