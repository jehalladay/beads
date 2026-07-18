package dolt

import (
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestGetReadyWorkLabelsAnyE2E is the end-to-end teeth for beads-mz2p:
// `bd ready --label-any X,Y` flows into WorkFilter.LabelsAny, and the wisp
// sub-path honored it, but BuildReadyWorkWhere (the main ready-issues query)
// never consumed it — so LabelsAny silently returned ALL ready issues. This
// drives a real embedded store to prove the filter actually narrows the
// ready-work result set to issues carrying at least one requested label.
func TestGetReadyWorkLabelsAnyE2E(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx, cancel := testContext(t)
	defer cancel()

	mk := func(id, label string) {
		iss := &types.Issue{ID: id, Title: id, Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask}
		if err := store.CreateIssue(ctx, iss, "tester"); err != nil {
			t.Fatalf("create %q: %v", id, err)
		}
		if err := store.AddLabel(ctx, iss.ID, label, "tester"); err != nil {
			t.Fatalf("label %q: %v", id, err)
		}
	}
	mk("ra-bug", "bug")
	mk("ra-feat", "feature")
	mk("ra-chore", "chore")

	t.Run("label-any narrows to the OR-set", func(t *testing.T) {
		got, err := store.GetReadyWork(ctx, types.WorkFilter{LabelsAny: []string{"bug", "feature"}})
		if err != nil {
			t.Fatalf("GetReadyWork(LabelsAny): %v", err)
		}
		ids := map[string]bool{}
		for _, i := range got {
			ids[i.ID] = true
		}
		if len(got) != 2 || !ids["ra-bug"] || !ids["ra-feat"] {
			t.Errorf("label-any {bug,feature} returned %d issues %v, want exactly ra-bug+ra-feat (was the filter silently ignored?)", len(got), ids)
		}
		if ids["ra-chore"] {
			t.Errorf("label-any leaked a non-matching issue (ra-chore)")
		}
	})

	t.Run("label-any is case-insensitive", func(t *testing.T) {
		got, err := store.GetReadyWork(ctx, types.WorkFilter{LabelsAny: []string{"BUG"}})
		if err != nil {
			t.Fatalf("GetReadyWork(LabelsAny BUG): %v", err)
		}
		if len(got) != 1 || got[0].ID != "ra-bug" {
			t.Errorf("case-insensitive label-any 'BUG' returned %d %v, want ra-bug", len(got), got)
		}
	})
}
