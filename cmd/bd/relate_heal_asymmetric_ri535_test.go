//go:build cgo

package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/types"
)

// TestRelateHealsAsymmetricLink_ri535 covers beads-ri535: `bd relate` on an
// ASYMMETRIC relates-to link (only id1->id2 present, e.g. from an imported
// one-sided row) must HEAL the missing reciprocal (id2->id1) instead of
// short-circuiting "already related, no change". The no-op guard is gated on a
// FULLY-bidirectional check, so an asymmetric link falls through to the atomic
// 2-dep add (AddDependency is idempotent → the present edge is a no-op, the
// missing one gets written).
func TestRelateHealsAsymmetricLink_ri535(t *testing.T) {
	tmpDir := t.TempDir()
	testDB := filepath.Join(tmpDir, ".beads", "beads.db")
	s := newTestStore(t, testDB)
	ctx := context.Background()

	for _, id := range []string{"test-rh-1", "test-rh-2"} {
		if err := s.CreateIssue(ctx, &types.Issue{
			ID: id, Title: id, Status: types.StatusOpen, Priority: 2,
			IssueType: types.TypeTask, CreatedAt: time.Now(),
		}, "test"); err != nil {
			t.Fatalf("CreateIssue %s: %v", id, err)
		}
	}

	// Seed an ASYMMETRIC link: only test-rh-1 -> test-rh-2 (no reciprocal),
	// simulating an imported one-sided relates-to row.
	if err := s.AddDependency(ctx, &types.Dependency{
		IssueID: "test-rh-1", DependsOnID: "test-rh-2", Type: types.DepRelatesTo,
	}, "test"); err != nil {
		t.Fatalf("seed asymmetric edge: %v", err)
	}

	prevStore, prevCtx, prevJSON := store, rootCtx, jsonOutput
	store = s
	rootCtx = ctx
	jsonOutput = false
	t.Cleanup(func() { store, rootCtx, jsonOutput = prevStore, prevCtx, prevJSON })

	// Sanity: the reciprocal is missing before the heal.
	if has, _ := hasRelatesEdge(ctx, s, "test-rh-2", "test-rh-1"); has {
		t.Fatalf("test setup: reciprocal edge unexpectedly already present")
	}

	out := captureStdout(t, func() error {
		return runRelate(relateCmd, []string{"test-rh-1", "test-rh-2"})
	})

	// It must NOT falsely no-op on the asymmetric link.
	if strings.Contains(out, "no change") {
		t.Errorf("relate on an asymmetric link falsely reported 'no change' (beads-ri535) — did not heal:\n%s", out)
	}

	// The missing reciprocal must now exist (healed).
	if has, err := hasRelatesEdge(ctx, s, "test-rh-2", "test-rh-1"); err != nil {
		t.Fatalf("check reciprocal: %v", err)
	} else if !has {
		t.Errorf("relate did not heal the missing reciprocal edge test-rh-2 -> test-rh-1 (beads-ri535)")
	}
	// The original direction must still exist.
	if has, _ := hasRelatesEdge(ctx, s, "test-rh-1", "test-rh-2"); !has {
		t.Errorf("relate dropped the original edge test-rh-1 -> test-rh-2")
	}
}

// hasRelatesEdge reports whether a relates-to edge from->to exists in the store.
func hasRelatesEdge(ctx context.Context, s interface {
	GetDependencyRecords(context.Context, string) ([]*types.Dependency, error)
}, from, to string) (bool, error) {
	recs, err := s.GetDependencyRecords(ctx, from)
	if err != nil {
		return false, err
	}
	for _, rec := range recs {
		if rec != nil && rec.DependsOnID == to && rec.Type == types.DepRelatesTo {
			return true, nil
		}
	}
	return false, nil
}
