package storage

import (
	"context"
	"errors"
	"testing"

	"github.com/steveyegge/beads/internal/hooks"
	"github.com/steveyegge/beads/internal/types"
)

// errHookStore is a fake DoltStorage whose CreateIssue / CreateIssues return
// createErr, driving the inner-error passthrough of HookFiringStore's create
// methods (no hook fires when the inner write fails).
type errHookStore struct {
	DoltStorage
	createErr error
}

func (s errHookStore) CreateIssue(_ context.Context, _ *types.Issue, _ string) error {
	return s.createErr
}

func (s errHookStore) CreateIssues(_ context.Context, _ []*types.Issue, _ string) error {
	return s.createErr
}

func TestHookFiringStoreCreateIssueErrorPassthrough(t *testing.T) {
	wantErr := errors.New("create failed")
	runner := &recordingHookRunner{}
	inner := errHookStore{createErr: wantErr}
	store := &HookFiringStore{DoltStorage: inner, inner: inner, runner: runner}

	if err := store.CreateIssue(context.Background(), &types.Issue{ID: "x"}, "tester"); !errors.Is(err, wantErr) {
		t.Fatalf("CreateIssue err = %v, want %v", err, wantErr)
	}
	if len(runner.events) != 0 {
		t.Fatalf("CreateIssue fired %d hooks on failure, want 0", len(runner.events))
	}
}

func TestHookFiringStoreCreateIssuesErrorPassthrough(t *testing.T) {
	wantErr := errors.New("create-many failed")
	runner := &recordingHookRunner{}
	inner := errHookStore{createErr: wantErr}
	store := &HookFiringStore{DoltStorage: inner, inner: inner, runner: runner}

	err := store.CreateIssues(context.Background(), []*types.Issue{{ID: "x"}}, "tester")
	if !errors.Is(err, wantErr) {
		t.Fatalf("CreateIssues err = %v, want %v", err, wantErr)
	}
	if len(runner.events) != 0 {
		t.Fatalf("CreateIssues fired %d hooks on failure, want 0", len(runner.events))
	}
}

func TestNewHookFiringStoreRealRunnerStored(t *testing.T) {
	// A non-nil *hooks.Runner takes the r=runner branch and is stored.
	runner := &hooks.Runner{}
	inner := fakeHookStore{}
	store := NewHookFiringStore(inner, runner)
	if store.runner == nil {
		t.Fatal("NewHookFiringStore with a real runner left runner nil")
	}
}

func TestCreateHookEventsNilIssue(t *testing.T) {
	if got := createHookEvents(nil); got != nil {
		t.Fatalf("createHookEvents(nil) = %v, want nil", got)
	}
}

func TestCloneIssueForHookNil(t *testing.T) {
	if got := cloneIssueForHook(nil); got != nil {
		t.Fatalf("cloneIssueForHook(nil) = %v, want nil", got)
	}
}

func TestCloneIssueForHookSkipsNilComment(t *testing.T) {
	issue := &types.Issue{
		ID:       "with-comments",
		Comments: []*types.Comment{nil, {ID: "1", Text: "kept"}},
	}
	clone := cloneIssueForHook(issue)
	if len(clone.Comments) != 2 {
		t.Fatalf("clone comment count = %d, want 2", len(clone.Comments))
	}
	if clone.Comments[0] != nil {
		t.Fatalf("nil comment should stay nil, got %+v", clone.Comments[0])
	}
	if clone.Comments[1] == nil || clone.Comments[1].Text != "kept" {
		t.Fatalf("non-nil comment not cloned: %+v", clone.Comments[1])
	}
	// Mutating the clone must not affect the source.
	clone.Comments[1].Text = "changed"
	if issue.Comments[1].Text != "kept" {
		t.Fatalf("mutating clone comment changed source to %q", issue.Comments[1].Text)
	}
}

func TestCloneDependenciesForHookNil(t *testing.T) {
	if got := cloneDependenciesForHook(nil); got != nil {
		t.Fatalf("cloneDependenciesForHook(nil) = %v, want nil", got)
	}
}

func TestCloneDependenciesForHookSkipsNilElement(t *testing.T) {
	deps := []*types.Dependency{nil, {IssueID: "a", DependsOnID: "b", Type: types.DepBlocks}}
	got := cloneDependenciesForHook(deps)
	if len(got) != 2 {
		t.Fatalf("clone length = %d, want 2", len(got))
	}
	if got[0] != nil {
		t.Fatalf("nil dependency should stay nil, got %+v", got[0])
	}
	if got[1] == nil || got[1].DependsOnID != "b" {
		t.Fatalf("non-nil dependency not cloned: %+v", got[1])
	}
}

func TestSameDependencyNilReturnsFalse(t *testing.T) {
	real := &types.Dependency{IssueID: "a", DependsOnID: "b", Type: types.DepBlocks}
	if sameDependency(nil, real, "a") {
		t.Fatal("sameDependency(nil, real) = true, want false")
	}
	if sameDependency(real, nil, "a") {
		t.Fatal("sameDependency(real, nil) = true, want false")
	}
}

func TestSameDependencyEmptyRequestedIssueIDFallback(t *testing.T) {
	persisted := &types.Dependency{IssueID: "issue-1", DependsOnID: "issue-2", Type: types.DepBlocks}
	// requested has an empty IssueID → falls back to the issueID arg.
	requested := &types.Dependency{IssueID: "", DependsOnID: "issue-2", Type: types.DepBlocks}
	if !sameDependency(persisted, requested, "issue-1") {
		t.Fatal("sameDependency with empty requested IssueID should fall back to issueID and match")
	}
	if sameDependency(persisted, requested, "other") {
		t.Fatal("sameDependency should not match when the fallback issueID differs")
	}
}

func TestDependencyHookEventsSkipsNilAndEmptyIDs(t *testing.T) {
	ctx := context.Background()
	get := func(context.Context, string) (*types.Issue, error) {
		return nil, errors.New("should not be called")
	}
	getDeps := func(context.Context, string) ([]*types.Dependency, error) {
		return nil, errors.New("should not be called")
	}

	issues := []*types.Issue{
		nil, // nil issue skipped
		{
			ID: "", // empty issue ID; dep also has empty IssueID → skipped
			Dependencies: []*types.Dependency{
				nil,                     // nil dependency skipped
				{DependsOnID: "target"}, // resolves to issueID "" → skipped
			},
		},
	}

	if got := dependencyHookEvents(ctx, issues, get, getDeps); len(got) != 0 {
		t.Fatalf("dependencyHookEvents = %v, want no events (all skipped)", got)
	}
}

func TestDependencyHookEventsSkipsOnSnapshotError(t *testing.T) {
	ctx := context.Background()
	get := func(context.Context, string) (*types.Issue, error) {
		return nil, errors.New("snapshot failed")
	}
	getDeps := func(context.Context, string) ([]*types.Dependency, error) {
		return nil, nil
	}
	issues := []*types.Issue{{
		ID: "source",
		Dependencies: []*types.Dependency{
			{IssueID: "source", DependsOnID: "target", Type: types.DepBlocks},
		},
	}}

	if got := dependencyHookEvents(ctx, issues, get, getDeps); len(got) != 0 {
		t.Fatalf("dependencyHookEvents = %v, want no events when snapshot errors", got)
	}
}
