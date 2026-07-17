package main

import (
	"context"
	"testing"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
)

// beads-s9rp: hermetic test for buildReadyIssueOutput (ready.go), which
// assembles []*IssueWithCounts from three store lookups + a parent-child
// derivation. A fake DoltStorage (embedded nil interface, three methods
// overridden) exercises it without a live Dolt.

type fakeReadyStore struct {
	storage.DoltStorage // nil — panics on any non-overridden method
	depCounts           map[string]*types.DependencyCounts
	depRecords          map[string][]*types.Dependency
	commentCounts       map[string]int
}

func (f *fakeReadyStore) GetDependencyCounts(_ context.Context, _ []string) (map[string]*types.DependencyCounts, error) {
	return f.depCounts, nil
}

func (f *fakeReadyStore) GetDependencyRecordsForIssues(_ context.Context, _ []string) (map[string][]*types.Dependency, error) {
	return f.depRecords, nil
}

func (f *fakeReadyStore) GetCommentCounts(_ context.Context, _ []string) (map[string]int, error) {
	return f.commentCounts, nil
}

func TestBuildReadyIssueOutput(t *testing.T) {
	ctx := context.Background()

	t.Run("nil issues → empty slice, no panic", func(t *testing.T) {
		f := &fakeReadyStore{}
		out := buildReadyIssueOutput(ctx, f, nil)
		if len(out) != 0 {
			t.Errorf("expected empty output, got %d", len(out))
		}
	})

	t.Run("assembles counts, comments, and parent", func(t *testing.T) {
		f := &fakeReadyStore{
			depCounts: map[string]*types.DependencyCounts{
				"a": {DependencyCount: 2, DependentCount: 1},
				// "b" intentionally absent → defaults to zeroes.
			},
			depRecords: map[string][]*types.Dependency{
				"a": {
					{DependsOnID: "p", Type: types.DepParentChild},
					{DependsOnID: "x", Type: types.DepBlocks},
				},
			},
			commentCounts: map[string]int{"a": 3},
		}
		issues := []*types.Issue{{ID: "a", Title: "A"}, {ID: "b", Title: "B"}}
		out := buildReadyIssueOutput(ctx, f, issues)
		if len(out) != 2 {
			t.Fatalf("expected 2 outputs, got %d", len(out))
		}

		// "a": counts + comments + parent from the parent-child dep.
		a := out[0]
		if a.DependencyCount != 2 || a.DependentCount != 1 || a.CommentCount != 3 {
			t.Errorf("a counts wrong: %+v", a)
		}
		if a.Parent == nil || *a.Parent != "p" {
			t.Errorf("a.Parent = %v, want p (from parent-child dep)", a.Parent)
		}
		// Dependencies are threaded onto the issue.
		if len(a.Issue.Dependencies) != 2 {
			t.Errorf("a.Dependencies = %d, want 2", len(a.Issue.Dependencies))
		}

		// "b": missing from all maps → zero counts, nil parent.
		b := out[1]
		if b.DependencyCount != 0 || b.DependentCount != 0 || b.CommentCount != 0 {
			t.Errorf("b should have zero counts, got %+v", b)
		}
		if b.Parent != nil {
			t.Errorf("b.Parent = %v, want nil (no parent-child dep)", b.Parent)
		}
	})

	t.Run("non-parent-child deps do not set a parent", func(t *testing.T) {
		f := &fakeReadyStore{
			depRecords: map[string][]*types.Dependency{
				"a": {{DependsOnID: "x", Type: types.DepBlocks}, {DependsOnID: "y", Type: types.DepRelated}},
			},
		}
		out := buildReadyIssueOutput(ctx, f, []*types.Issue{{ID: "a"}})
		if out[0].Parent != nil {
			t.Errorf("Parent should be nil when no parent-child dep exists, got %v", out[0].Parent)
		}
	})
}
