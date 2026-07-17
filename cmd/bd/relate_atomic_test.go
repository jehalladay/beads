//go:build cgo

package main

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
)

// TestRelateIsAtomic_oyy1 is the teeth for beads-oyy1: `bd dep relate` writes
// the two reciprocal relates-to deps in ONE transaction, so a mid-op failure
// on the second write rolls back the first — never leaving a half (asymmetric)
// relation where id1->id2 exists but id2->id1 doesn't.
//
// It exercises the exact transactional shape runRelate now uses (RunInTransaction
// with two tx.AddDependency calls) and forces the SECOND write to fail, then
// asserts the FIRST did not persist.
func TestRelateIsAtomic_oyy1(t *testing.T) {
	tmpDir := t.TempDir()
	store := newTestStore(t, filepath.Join(tmpDir, ".beads", "beads.db"))
	ctx := context.Background()

	mk := func(id string) {
		if err := store.CreateIssue(ctx, &types.Issue{
			ID: id, Title: id, Status: types.StatusOpen,
			Priority: 1, IssueType: types.TypeTask,
		}, "test"); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}
	mk("ra-1")
	mk("ra-2")

	// Pre-seed a CONFLICTING dep for the second direction (ra-2 -> ra-1 with a
	// DIFFERENT type). AddDependency errors when the same edge already exists
	// with a different type, so the second write inside the relate tx will fail.
	if err := store.AddDependency(ctx, &types.Dependency{
		IssueID: "ra-2", DependsOnID: "ra-1", Type: types.DepBlocks,
	}, "test"); err != nil {
		t.Fatalf("seed conflicting dep: %v", err)
	}

	// Mirror runRelate's transactional write: dep1 (ra-1->ra-2 relates-to) then
	// dep2 (ra-2->ra-1 relates-to) — dep2 must fail on the type conflict.
	dep1 := &types.Dependency{IssueID: "ra-1", DependsOnID: "ra-2", Type: types.DepRelatesTo}
	dep2 := &types.Dependency{IssueID: "ra-2", DependsOnID: "ra-1", Type: types.DepRelatesTo}
	err := store.RunInTransaction(ctx, "test: relate ra-1 <-> ra-2", func(tx storage.Transaction) error {
		if aerr := tx.AddDependency(ctx, dep1, "test"); aerr != nil {
			return fmt.Errorf("dep1: %w", aerr)
		}
		if aerr := tx.AddDependency(ctx, dep2, "test"); aerr != nil {
			return fmt.Errorf("dep2: %w", aerr)
		}
		return nil
	})
	if err == nil {
		t.Fatal("expected the relate tx to fail on the conflicting second edge")
	}

	// ATOMICITY: because dep2 failed, dep1 (ra-1 -> ra-2 relates-to) must NOT
	// have persisted — no half relation.
	deps, derr := store.GetDependenciesWithMetadata(ctx, "ra-1")
	if derr != nil {
		t.Fatalf("get deps for ra-1: %v", derr)
	}
	for _, d := range deps {
		if d.ID == "ra-2" && d.DependencyType == types.DepRelatesTo {
			t.Errorf("REGRESSION (oyy1): ra-1->ra-2 relates-to persisted despite the tx failing — half (asymmetric) relation left behind")
		}
	}
}
