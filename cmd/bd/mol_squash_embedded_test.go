//go:build cgo

package main

import (
	"context"
	"os"
	"testing"

	"github.com/steveyegge/beads/internal/storage/embeddeddolt"
	"github.com/steveyegge/beads/internal/types"
)

// TestEmbeddedSquashWispMoleculeClearsRootEphemeral mirrors
// TestSquashWispMoleculeClearsRootEphemeral but runs against the embedded
// Dolt backend used by CI. Without this, the standalone-Dolt regression test
// skips in the Embedded Dolt CI tier and the fix's lines stay uncovered.
func TestEmbeddedSquashWispMoleculeClearsRootEphemeral(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}

	ctx := context.Background()
	beadsDir := t.TempDir()

	s, err := embeddeddolt.Open(ctx, beadsDir, "moltest", "main")
	if err != nil {
		t.Fatalf("embeddeddolt.Open: %v", err)
	}
	defer s.Close()

	if err := s.SetConfig(ctx, "issue_prefix", "test"); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}

	root := &types.Issue{
		Title:     "Wisp Molecule Root",
		Status:    types.StatusOpen,
		Priority:  1,
		IssueType: types.TypeEpic,
		Ephemeral: true,
	}
	if err := s.CreateIssue(ctx, root, "test"); err != nil {
		t.Fatalf("CreateIssue root: %v", err)
	}

	child := &types.Issue{
		Title:     "Wisp Step",
		Status:    types.StatusClosed,
		Priority:  2,
		IssueType: types.TypeTask,
		Ephemeral: true,
	}
	if err := s.CreateIssue(ctx, child, "test"); err != nil {
		t.Fatalf("CreateIssue child: %v", err)
	}
	if err := s.AddDependency(ctx, &types.Dependency{
		IssueID:     child.ID,
		DependsOnID: root.ID,
		Type:        types.DepParentChild,
	}, "test"); err != nil {
		t.Fatalf("AddDependency: %v", err)
	}

	result, err := squashMolecule(ctx, s, root, []*types.Issue{child}, false, "", "test")
	if err != nil {
		t.Fatalf("squashMolecule: %v", err)
	}
	if !result.WispSquash {
		t.Error("expected WispSquash=true for ephemeral root")
	}

	closed, err := s.GetIssue(ctx, root.ID)
	if err != nil {
		t.Fatalf("GetIssue root after squash: %v", err)
	}
	if closed.Status != types.StatusClosed {
		t.Errorf("root status = %v, want closed", closed.Status)
	}
	if closed.Ephemeral {
		t.Error("root Ephemeral should be false after squash; closed wisp roots otherwise leak as duplicate JSONL rows on every export cycle")
	}
}

// TestEmbeddedSquashKeepChildrenClearsWispFlag covers beads-ho61c: `bd mol
// squash --keep-children` must PROMOTE each preserved child to persistent by
// clearing its Wisp/Ephemeral flag (the help promises "promotes ... clearing
// their Wisp flag"). Before the fix, --keep-children only skipped deletion —
// each kept child stayed ephemeral=true and would be silently reaped by a
// later `bd mol wisp gc`, destroying the very trace the user asked to keep.
// RED-verify by deleting the else-branch in squashMolecule: the kept children
// stay Ephemeral=true and this test fails.
func TestEmbeddedSquashKeepChildrenClearsWispFlag(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}

	ctx := context.Background()
	beadsDir := t.TempDir()

	s, err := embeddeddolt.Open(ctx, beadsDir, "moltest", "main")
	if err != nil {
		t.Fatalf("embeddeddolt.Open: %v", err)
	}
	defer s.Close()

	if err := s.SetConfig(ctx, "issue_prefix", "test"); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}

	// A non-ephemeral root (a --label template epic) so the root-clear branch
	// (gated on root.Ephemeral) does NOT fire — this isolates the child-clear
	// path introduced for --keep-children.
	root := &types.Issue{
		Title:     "Template Molecule Root",
		Status:    types.StatusOpen,
		Priority:  1,
		IssueType: types.TypeEpic,
		Ephemeral: false,
	}
	if err := s.CreateIssue(ctx, root, "test"); err != nil {
		t.Fatalf("CreateIssue root: %v", err)
	}

	children := make([]*types.Issue, 2)
	for i := range children {
		c := &types.Issue{
			Title:     "Wisp Step",
			Status:    types.StatusClosed,
			Priority:  2,
			IssueType: types.TypeTask,
			Ephemeral: true,
		}
		if err := s.CreateIssue(ctx, c, "test"); err != nil {
			t.Fatalf("CreateIssue child %d: %v", i, err)
		}
		if err := s.AddDependency(ctx, &types.Dependency{
			IssueID:     c.ID,
			DependsOnID: root.ID,
			Type:        types.DepParentChild,
		}, "test"); err != nil {
			t.Fatalf("AddDependency child %d: %v", i, err)
		}
		children[i] = c
	}

	result, err := squashMolecule(ctx, s, root, children, true /* keepChildren */, "", "test")
	if err != nil {
		t.Fatalf("squashMolecule: %v", err)
	}
	if !result.KeptChildren {
		t.Error("expected KeptChildren=true")
	}
	if result.DeletedCount != 0 {
		t.Errorf("DeletedCount = %d, want 0 (--keep-children must not delete)", result.DeletedCount)
	}

	for _, c := range children {
		got, err := s.GetIssue(ctx, c.ID)
		if err != nil {
			// A "not found" here means the child was deleted — the exact
			// data-loss beads-ho61c is about — so surface it explicitly.
			t.Fatalf("GetIssue kept child %s after squash: %v (child must survive --keep-children)", c.ID, err)
		}
		if got.Ephemeral {
			t.Errorf("kept child %s Ephemeral = true after --keep-children squash; want false (beads-ho61c: preserved children must be promoted to persistent or `bd mol wisp gc` silently reaps them)", c.ID)
		}
	}
}
