package main

import (
	"context"
	"testing"

	"github.com/steveyegge/beads/internal/storage/domain"
	"github.com/steveyegge/beads/internal/storage/uow"
	"github.com/steveyegge/beads/internal/types"
)

// beads-5yzu: hermetic test for buildReadyIssueOutputProxied — the proxied twin
// of buildReadyIssueOutput, reading through a uow.UnitOfWork's use-cases instead
// of a DoltStorage. Fakes embed the interfaces (nil) and override only the two
// dependency methods + the comment-count method the function calls.

type fakeDepUC struct {
	domain.DependencyUseCase // nil — panics on any non-overridden method
	counts                   map[string]*types.DependencyCounts
	records                  map[string][]*types.Dependency
}

func (f *fakeDepUC) CountsByIssueIDs(_ context.Context, _ []string) (map[string]*types.DependencyCounts, error) {
	return f.counts, nil
}

func (f *fakeDepUC) GetForIssueIDs(_ context.Context, _ []string) (map[string][]*types.Dependency, error) {
	return f.records, nil
}

type fakeCommentUC struct {
	domain.CommentUseCase // nil
	counts                map[string]int
}

func (f *fakeCommentUC) GetCommentCounts(_ context.Context, _ []string) (map[string]int, error) {
	return f.counts, nil
}

type fakeReadyUOW struct {
	uow.UnitOfWork // nil — panics on any non-overridden method
	dep            *fakeDepUC
	comment        *fakeCommentUC
}

func (f *fakeReadyUOW) DependencyUseCase() domain.DependencyUseCase { return f.dep }
func (f *fakeReadyUOW) CommentUseCase() domain.CommentUseCase       { return f.comment }

func TestBuildReadyIssueOutputProxied(t *testing.T) {
	ctx := context.Background()

	t.Run("nil issues → empty slice, no panic", func(t *testing.T) {
		uw := &fakeReadyUOW{dep: &fakeDepUC{}, comment: &fakeCommentUC{}}
		if out := buildReadyIssueOutputProxied(ctx, uw, nil); len(out) != 0 {
			t.Errorf("expected empty, got %d", len(out))
		}
	})

	t.Run("assembles counts, comments, and parent", func(t *testing.T) {
		uw := &fakeReadyUOW{
			dep: &fakeDepUC{
				counts: map[string]*types.DependencyCounts{"a": {DependencyCount: 2, DependentCount: 1}},
				records: map[string][]*types.Dependency{
					"a": {
						{DependsOnID: "p", Type: types.DepParentChild},
						{DependsOnID: "x", Type: types.DepBlocks},
					},
				},
			},
			comment: &fakeCommentUC{counts: map[string]int{"a": 3}},
		}
		issues := []*types.Issue{{ID: "a", Title: "A"}, {ID: "b", Title: "B"}}
		out := buildReadyIssueOutputProxied(ctx, uw, issues)
		if len(out) != 2 {
			t.Fatalf("expected 2 outputs, got %d", len(out))
		}
		a := out[0]
		if a.DependencyCount != 2 || a.DependentCount != 1 || a.CommentCount != 3 {
			t.Errorf("a counts wrong: %+v", a)
		}
		if a.Parent == nil || *a.Parent != "p" {
			t.Errorf("a.Parent = %v, want p", a.Parent)
		}
		if len(a.Issue.Dependencies) != 2 {
			t.Errorf("a.Dependencies = %d, want 2", len(a.Issue.Dependencies))
		}
		// "b" absent from all maps → zero counts, nil parent.
		b := out[1]
		if b.DependencyCount != 0 || b.CommentCount != 0 || b.Parent != nil {
			t.Errorf("b should be zero/nil, got %+v (parent %v)", b, b.Parent)
		}
	})

	t.Run("non-parent-child deps leave parent nil", func(t *testing.T) {
		uw := &fakeReadyUOW{
			dep: &fakeDepUC{records: map[string][]*types.Dependency{
				"a": {{DependsOnID: "x", Type: types.DepBlocks}},
			}},
			comment: &fakeCommentUC{},
		}
		out := buildReadyIssueOutputProxied(ctx, uw, []*types.Issue{{ID: "a"}})
		if out[0].Parent != nil {
			t.Errorf("Parent should be nil, got %v", out[0].Parent)
		}
	})
}
