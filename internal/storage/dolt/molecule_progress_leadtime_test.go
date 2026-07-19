package dolt

import (
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/types"
)

// TestGetMoleculeProgress_FirstLastClosed is the behavioral teeth for beads-tcx7:
// types.MoleculeProgressStats.FirstClosed/LastClosed were declared and consumed
// (mol_progress.go computes rate_per_hour/eta_hours + the human "Rate: ~N
// steps/hour" ONLY when FirstClosed!=nil && LastClosed!=nil && Completed>1) but
// NO producer ever assigned them → the guard was never true → the entire rate/ETA
// feature was dead. This runs the real embedded store end-to-end (a pure test
// would false-green: it cannot prove the closed_at column round-trips through the
// engine into FirstClosed/LastClosed). Two children are closed with distinct
// backdated closed_at values so FirstClosed<LastClosed and the rate is computable.
func TestGetMoleculeProgress_FirstLastClosed(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()
	ctx, cancel := testContext(t)
	defer cancel()

	// A molecule (parent) with two child steps.
	parent := &types.Issue{ID: "mp-root", Title: "Molecule", Status: types.StatusOpen, Priority: 1, IssueType: types.TypeTask}
	c1 := &types.Issue{ID: "mp-c1", Title: "Step 1", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask}
	c2 := &types.Issue{ID: "mp-c2", Title: "Step 2", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask}
	for _, iss := range []*types.Issue{parent, c1, c2} {
		if err := store.CreateIssue(ctx, iss, "tester"); err != nil {
			t.Fatalf("create %s: %v", iss.ID, err)
		}
	}
	for _, child := range []string{"mp-c1", "mp-c2"} {
		if err := store.AddDependency(ctx, &types.Dependency{
			IssueID: child, DependsOnID: "mp-root", Type: types.DepParentChild,
		}, "tester"); err != nil {
			t.Fatalf("AddDependency %s: %v", child, err)
		}
	}

	// Before any closure: FirstClosed/LastClosed unset, feature still dead (expected).
	stats, err := store.GetMoleculeProgress(ctx, "mp-root")
	if err != nil {
		t.Fatalf("GetMoleculeProgress (pre-close): %v", err)
	}
	if stats.FirstClosed != nil || stats.LastClosed != nil {
		t.Errorf("pre-close FirstClosed/LastClosed = %v/%v, want nil/nil", stats.FirstClosed, stats.LastClosed)
	}

	// Close both children, then backdate their closed_at to two DISTINCT times so
	// the min/max is unambiguous and the rate has a positive duration.
	if err := store.CloseIssue(ctx, "mp-c1", "done", "tester", ""); err != nil {
		t.Fatalf("close mp-c1: %v", err)
	}
	if err := store.CloseIssue(ctx, "mp-c2", "done", "tester", ""); err != nil {
		t.Fatalf("close mp-c2: %v", err)
	}
	early := time.Now().UTC().Add(-10 * time.Hour)
	late := time.Now().UTC().Add(-2 * time.Hour)
	if _, err := store.db.ExecContext(ctx, "UPDATE issues SET closed_at = ? WHERE id = ?", early, "mp-c1"); err != nil {
		t.Fatalf("backdate mp-c1: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, "UPDATE issues SET closed_at = ? WHERE id = ?", late, "mp-c2"); err != nil {
		t.Fatalf("backdate mp-c2: %v", err)
	}

	stats, err = store.GetMoleculeProgress(ctx, "mp-root")
	if err != nil {
		t.Fatalf("GetMoleculeProgress (post-close): %v", err)
	}

	if stats.Completed != 2 {
		t.Fatalf("Completed = %d, want 2", stats.Completed)
	}
	if stats.FirstClosed == nil || stats.LastClosed == nil {
		t.Fatalf("FirstClosed/LastClosed = %v/%v, want both non-nil (the beads-tcx7 dead-feature bug)", stats.FirstClosed, stats.LastClosed)
	}
	// FirstClosed must be the EARLIER timestamp, LastClosed the later — proving
	// min/max tracking, not just "assigned the first row seen".
	if !stats.LastClosed.After(*stats.FirstClosed) {
		t.Errorf("expected LastClosed (%v) strictly after FirstClosed (%v)", stats.LastClosed, stats.FirstClosed)
	}
	// The span must be ~8h (early=-10h, late=-2h). Allow slack for round-trip
	// truncation; the point is a positive, meaningful duration that makes the
	// mol_progress.go rate guard (duration > 0) true.
	span := stats.LastClosed.Sub(*stats.FirstClosed)
	if span < 6*time.Hour || span > 10*time.Hour {
		t.Errorf("closure span = %v, want ~8h (proves FirstClosed=min, LastClosed=max round-tripped)", span)
	}
}
