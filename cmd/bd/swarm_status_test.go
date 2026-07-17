package main

import (
	"context"
	"errors"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// beads-ybn8: hermetic tests for getSwarmStatus (0%) via a fake SwarmStorage.
// Uses a distinctly-named fake (fakeStatusStore) so it can coexist in the
// package with the sibling swarm_storage_test.go fake (beads-cgyn) if both land.

type fakeStatusStore struct {
	issues     map[string]*types.Issue
	dependents map[string][]*types.Issue
	depRecords map[string][]*types.Dependency
	dependErr  error
}

func newFakeStatusStore() *fakeStatusStore {
	return &fakeStatusStore{
		issues:     map[string]*types.Issue{},
		dependents: map[string][]*types.Issue{},
		depRecords: map[string][]*types.Dependency{},
	}
}

func (f *fakeStatusStore) GetIssue(_ context.Context, id string) (*types.Issue, error) {
	iss, ok := f.issues[id]
	if !ok {
		return nil, errors.New("not found")
	}
	return iss, nil
}

func (f *fakeStatusStore) GetDependents(_ context.Context, id string) ([]*types.Issue, error) {
	if f.dependErr != nil {
		return nil, f.dependErr
	}
	return f.dependents[id], nil
}

func (f *fakeStatusStore) GetDependencyRecords(_ context.Context, id string) ([]*types.Dependency, error) {
	return f.depRecords[id], nil
}

// pcChild marks an issue as a parent-child child of the epic in the fake.
func (f *fakeStatusStore) pcChild(epicID string, iss *types.Issue, extraDeps ...*types.Dependency) {
	f.dependents[epicID] = append(f.dependents[epicID], iss)
	f.issues[iss.ID] = iss
	deps := []*types.Dependency{{DependsOnID: epicID, Type: types.DepParentChild}}
	deps = append(deps, extraDeps...)
	f.depRecords[iss.ID] = deps
}

func TestGetSwarmStatus(t *testing.T) {
	ctx := context.Background()
	epic := &types.Issue{ID: "epic", Title: "Epic"}

	t.Run("GetDependents error propagates", func(t *testing.T) {
		f := newFakeStatusStore()
		f.dependErr = errors.New("boom")
		if _, err := getSwarmStatus(ctx, f, epic); err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("no children → empty status", func(t *testing.T) {
		f := newFakeStatusStore()
		st, err := getSwarmStatus(ctx, f, epic)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if st.TotalIssues != 0 || st.Progress != 0 {
			t.Fatalf("expected empty status, got %+v", st)
		}
	})

	t.Run("categorizes completed/active/ready/blocked with progress", func(t *testing.T) {
		f := newFakeStatusStore()
		// c: closed → Completed
		f.pcChild("epic", &types.Issue{ID: "c", Title: "c", Status: types.StatusClosed})
		// a: in_progress → Active
		f.pcChild("epic", &types.Issue{ID: "a", Title: "a", Status: types.StatusInProgress})
		// r: open, no blockers → Ready
		f.pcChild("epic", &types.Issue{ID: "r", Title: "r", Status: types.StatusOpen})
		// b: open, blocked by 'a' (in_progress, not closed) → Blocked
		f.pcChild("epic", &types.Issue{ID: "b", Title: "b", Status: types.StatusOpen},
			&types.Dependency{DependsOnID: "a", Type: types.DepBlocks})

		st, err := getSwarmStatus(ctx, f, epic)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if st.TotalIssues != 4 {
			t.Fatalf("TotalIssues = %d, want 4", st.TotalIssues)
		}
		if len(st.Completed) != 1 || st.Completed[0].ID != "c" {
			t.Errorf("Completed = %+v, want [c]", st.Completed)
		}
		if st.ActiveCount != 1 || st.Active[0].ID != "a" {
			t.Errorf("Active = %+v, want [a]", st.Active)
		}
		if st.ReadyCount != 1 || st.Ready[0].ID != "r" {
			t.Errorf("Ready = %+v, want [r]", st.Ready)
		}
		if st.BlockedCount != 1 || st.Blocked[0].ID != "b" {
			t.Errorf("Blocked = %+v, want [b]", st.Blocked)
		}
		if len(st.Blocked) == 1 && (len(st.Blocked[0].BlockedBy) != 1 || st.Blocked[0].BlockedBy[0] != "a") {
			t.Errorf("Blocked[0].BlockedBy = %v, want [a]", st.Blocked[0].BlockedBy)
		}
		// 1 of 4 complete → 25%.
		if st.Progress != 25 {
			t.Errorf("Progress = %v, want 25", st.Progress)
		}
	})

	t.Run("open issue whose blocker is closed becomes Ready", func(t *testing.T) {
		f := newFakeStatusStore()
		f.pcChild("epic", &types.Issue{ID: "done", Title: "done", Status: types.StatusClosed})
		f.pcChild("epic", &types.Issue{ID: "next", Title: "next", Status: types.StatusOpen},
			&types.Dependency{DependsOnID: "done", Type: types.DepBlocks})

		st, err := getSwarmStatus(ctx, f, epic)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		// 'next' depends only on a CLOSED issue → not blocked → Ready.
		if st.ReadyCount != 1 || st.Ready[0].ID != "next" {
			t.Errorf("expected next to be Ready, got ready=%+v blocked=%+v", st.Ready, st.Blocked)
		}
		if st.BlockedCount != 0 {
			t.Errorf("expected nothing blocked, got %+v", st.Blocked)
		}
	})
}
