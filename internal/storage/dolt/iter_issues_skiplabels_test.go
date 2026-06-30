//go:build cgo

package dolt

import (
	"testing"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
)

// TestIterIssuesSkipLabels verifies the SkipLabels filter added to the
// streaming IterIssues path for the sync OOM fix (beads-r06.13 / OOM-1).
//
// With SkipLabels=true the iterator must (a) not populate issue.Labels and
// (b) drain fully on a SINGLE-connection pool. setupTestStore uses
// MaxOpenConns=1, and the default per-row label hydration needs a SECOND
// connection while the cursor holds the first — that deadlocks (verified: it
// hits context-deadline). The sync engine streams with SkipLabels precisely so
// the cursor needs no concurrent second connection, then batch-hydrates labels
// after the cursor closes.
func TestIterIssuesSkipLabels(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()
	ctx, cancel := testContext(t)
	defer cancel()

	iss := &types.Issue{
		ID:        "bd-iterlbl1",
		Title:     "Labeled issue",
		Status:    types.StatusOpen,
		IssueType: types.TypeTask,
		Priority:  2,
		Labels:    []string{"alpha", "beta"},
	}
	if err := store.CreateIssue(ctx, iss, "test-actor"); err != nil {
		t.Fatalf("CreateIssue error: %v", err)
	}

	// SkipLabels=true: no labels, and (the key behavior) no deadlock on the
	// MaxOpenConns=1 pool.
	noLabels, err := collectIter(t, store, types.IssueFilter{SkipLabels: true})
	if err != nil {
		t.Fatalf("IterIssues(SkipLabels=true) error: %v", err)
	}
	got := findIssue(noLabels, "bd-iterlbl1")
	if got == nil {
		t.Fatal("issue not returned by IterIssues(SkipLabels=true)")
	}
	if len(got.Labels) != 0 {
		t.Errorf("SkipLabels=true: got %d labels, want 0 (%v)", len(got.Labels), got.Labels)
	}
}

func collectIter(t *testing.T, store *DoltStore, filter types.IssueFilter) ([]*types.Issue, error) {
	t.Helper()
	ctx, cancel := testContext(t)
	defer cancel()
	it, err := store.IterIssues(ctx, "", filter)
	if err != nil {
		return nil, err
	}
	return storage.Collect(ctx, it)
}

func findIssue(issues []*types.Issue, id string) *types.Issue {
	for _, iss := range issues {
		if iss.ID == id {
			return iss
		}
	}
	return nil
}
