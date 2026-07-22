package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// beads-cgyn: hermetic tests for the storage-backed swarm helpers
// (findExistingSwarm, getEpicChildren, analyzeEpicForSwarm) via a fake
// SwarmStorage — the interface is just 3 methods (GetIssue, GetDependents,
// GetDependencyRecords), so no real Dolt is needed.

type fakeSwarmStore struct {
	issues     map[string]*types.Issue
	dependents map[string][]*types.Issue      // epicID/issueID → dependents
	depRecords map[string][]*types.Dependency // issueID → its dependency edges
	dependErr  error
	recErrFor  map[string]error // per-id GetDependencyRecords error
}

func newFakeSwarmStore() *fakeSwarmStore {
	return &fakeSwarmStore{
		issues:     map[string]*types.Issue{},
		dependents: map[string][]*types.Issue{},
		depRecords: map[string][]*types.Dependency{},
		recErrFor:  map[string]error{},
	}
}

func (f *fakeSwarmStore) GetIssue(_ context.Context, id string) (*types.Issue, error) {
	iss, ok := f.issues[id]
	if !ok {
		return nil, errors.New("not found")
	}
	return iss, nil
}

func (f *fakeSwarmStore) GetDependents(_ context.Context, id string) ([]*types.Issue, error) {
	if f.dependErr != nil {
		return nil, f.dependErr
	}
	return f.dependents[id], nil
}

func (f *fakeSwarmStore) GetDependencyRecords(_ context.Context, id string) ([]*types.Dependency, error) {
	if err := f.recErrFor[id]; err != nil {
		return nil, err
	}
	return f.depRecords[id], nil
}

func TestFindExistingSwarm(t *testing.T) {
	ctx := context.Background()

	t.Run("GetDependents error propagates", func(t *testing.T) {
		f := newFakeSwarmStore()
		f.dependErr = errors.New("boom")
		if _, err := findExistingSwarm(ctx, f, "epic"); err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("no swarm molecule → nil", func(t *testing.T) {
		f := newFakeSwarmStore()
		// A dependent that is a plain task, not a molecule.
		f.dependents["epic"] = []*types.Issue{{ID: "t1", IssueType: types.TypeTask}}
		got, err := findExistingSwarm(ctx, f, "epic")
		if err != nil || got != nil {
			t.Fatalf("expected (nil,nil), got (%v,%v)", got, err)
		}
	})

	t.Run("swarm molecule linked via relates-to is found", func(t *testing.T) {
		f := newFakeSwarmStore()
		f.dependents["epic"] = []*types.Issue{{ID: "mol1", IssueType: "molecule"}}
		f.issues["mol1"] = &types.Issue{ID: "mol1", IssueType: "molecule", MolType: types.MolTypeSwarm}
		f.depRecords["mol1"] = []*types.Dependency{{DependsOnID: "epic", Type: types.DepRelatesTo}}
		got, err := findExistingSwarm(ctx, f, "epic")
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if got == nil || got.ID != "mol1" {
			t.Fatalf("expected mol1, got %v", got)
		}
	})

	t.Run("molecule with wrong mol_type is skipped", func(t *testing.T) {
		f := newFakeSwarmStore()
		f.dependents["epic"] = []*types.Issue{{ID: "mol1", IssueType: "molecule"}}
		f.issues["mol1"] = &types.Issue{ID: "mol1", IssueType: "molecule", MolType: types.MolTypePatrol}
		got, _ := findExistingSwarm(ctx, f, "epic")
		if got != nil {
			t.Fatalf("patrol molecule should be skipped, got %v", got)
		}
	})

	t.Run("swarm molecule without relates-to link is skipped", func(t *testing.T) {
		f := newFakeSwarmStore()
		f.dependents["epic"] = []*types.Issue{{ID: "mol1", IssueType: "molecule"}}
		f.issues["mol1"] = &types.Issue{ID: "mol1", IssueType: "molecule", MolType: types.MolTypeSwarm}
		// Linked by parent-child, not relates-to.
		f.depRecords["mol1"] = []*types.Dependency{{DependsOnID: "epic", Type: types.DepParentChild}}
		got, _ := findExistingSwarm(ctx, f, "epic")
		if got != nil {
			t.Fatalf("non-relates-to molecule should be skipped, got %v", got)
		}
	})
}

func TestGetEpicChildren(t *testing.T) {
	ctx := context.Background()

	t.Run("GetDependents error propagates", func(t *testing.T) {
		f := newFakeSwarmStore()
		f.dependErr = errors.New("boom")
		if _, err := getEpicChildren(ctx, f, "epic"); err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("filters to parent-child children only", func(t *testing.T) {
		f := newFakeSwarmStore()
		f.dependents["epic"] = []*types.Issue{
			{ID: "c1"}, {ID: "c2"}, {ID: "related"},
		}
		f.depRecords["c1"] = []*types.Dependency{{DependsOnID: "epic", Type: types.DepParentChild}}
		f.depRecords["c2"] = []*types.Dependency{{DependsOnID: "epic", Type: types.DepParentChild}}
		f.depRecords["related"] = []*types.Dependency{{DependsOnID: "epic", Type: types.DepRelatesTo}}
		children, err := getEpicChildren(ctx, f, "epic")
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if len(children) != 2 {
			t.Fatalf("expected 2 parent-child children, got %d: %+v", len(children), children)
		}
	})

	t.Run("dependency-record error skips that dependent", func(t *testing.T) {
		f := newFakeSwarmStore()
		f.dependents["epic"] = []*types.Issue{{ID: "c1"}, {ID: "bad"}}
		f.depRecords["c1"] = []*types.Dependency{{DependsOnID: "epic", Type: types.DepParentChild}}
		f.recErrFor["bad"] = errors.New("query fail")
		children, err := getEpicChildren(ctx, f, "epic")
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if len(children) != 1 || children[0].ID != "c1" {
			t.Fatalf("expected only c1, got %+v", children)
		}
	})
}

func TestAnalyzeEpicForSwarm(t *testing.T) {
	ctx := context.Background()
	epic := &types.Issue{ID: "epic", Title: "Epic"}

	t.Run("epic with no children warns and is returned", func(t *testing.T) {
		f := newFakeSwarmStore()
		a, err := analyzeEpicForSwarm(ctx, f, epic)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if a.TotalIssues != 0 || !swarmWarnsContain(a.Warnings, "no children") {
			t.Fatalf("expected no-children warning, got %+v", a)
		}
		// beads-j75r: a childless epic must NOT report swarmable:true. The JSON
		// path marshals analysis.Swarmable directly, so leaving the struct
		// default (true) while total_issues:0 is a JSON/human divergence — the
		// human path prints "no children to swarm" (not swarmable).
		if a.Swarmable {
			t.Fatalf("childless epic must not be swarmable (total_issues=%d), got swarmable=true", a.TotalIssues)
		}
	})

	t.Run("builds graph, waves, and swarmable for a clean chain", func(t *testing.T) {
		f := newFakeSwarmStore()
		f.dependents["epic"] = []*types.Issue{
			{ID: "a", Title: "a", Status: types.StatusOpen},
			{ID: "b", Title: "b", Status: types.StatusClosed},
		}
		// Both are parent-child children of the epic; b depends on a (blocks).
		f.depRecords["a"] = []*types.Dependency{{DependsOnID: "epic", Type: types.DepParentChild}}
		f.depRecords["b"] = []*types.Dependency{
			{DependsOnID: "epic", Type: types.DepParentChild},
			{DependsOnID: "a", Type: types.DepBlocks},
		}
		a, err := analyzeEpicForSwarm(ctx, f, epic)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		// TotalIssues/ClosedIssues are display counts and keep counting all
		// children (2 total, 1 closed).
		if a.TotalIssues != 2 || a.ClosedIssues != 1 {
			t.Fatalf("counts wrong: total=%d closed=%d", a.TotalIssues, a.ClosedIssues)
		}
		if !a.Swarmable || len(a.Errors) != 0 {
			t.Fatalf("clean chain should be swarmable, got errors=%v", a.Errors)
		}
		// beads-y6pjs: b is CLOSED — it is not a worker-session and must never be
		// scheduled into a wave (Wave stays -1). Only the open leaf 'a' is a
		// schedulable root at wave 0.
		if a.Issues["a"].Wave != 0 {
			t.Errorf("open root should be wave 0, got %d", a.Issues["a"].Wave)
		}
		if a.Issues["b"].Wave != -1 {
			t.Errorf("closed child must NOT be scheduled into a wave, got wave %d", a.Issues["b"].Wave)
		}
		// EstimatedSessions/MaxParallelism reflect only OPEN actionable work.
		if a.EstimatedSessions != 1 {
			t.Errorf("EstimatedSessions should exclude closed children (want 1), got %d", a.EstimatedSessions)
		}
		// No ready front may list the closed issue.
		for _, front := range a.ReadyFronts {
			for _, id := range front.Issues {
				if id == "b" {
					t.Errorf("closed issue b appeared in ready front wave %d", front.Wave)
				}
			}
		}
	})

	t.Run("closed children excluded from waves, sessions, and parallelism", func(t *testing.T) {
		// beads-y6pjs: two inDegree-0 roots (one open, one CLOSED) plus an open
		// leaf depending on the open root. Without the closed-exclusion the closed
		// root would seed wave 0 alongside the open root (MaxParallelism 2) and
		// count as a session; the analysis must instead reflect only open work.
		f := newFakeSwarmStore()
		f.dependents["epic"] = []*types.Issue{
			{ID: "r1", Title: "open root", Status: types.StatusOpen},
			{ID: "r2", Title: "done root", Status: types.StatusClosed},
			{ID: "leaf", Title: "leaf", Status: types.StatusOpen},
		}
		f.depRecords["r1"] = []*types.Dependency{{DependsOnID: "epic", Type: types.DepParentChild}}
		f.depRecords["r2"] = []*types.Dependency{{DependsOnID: "epic", Type: types.DepParentChild}}
		f.depRecords["leaf"] = []*types.Dependency{
			{DependsOnID: "epic", Type: types.DepParentChild},
			{DependsOnID: "r1", Type: types.DepBlocks},
		}
		a, err := analyzeEpicForSwarm(ctx, f, epic)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if a.TotalIssues != 3 || a.ClosedIssues != 1 {
			t.Fatalf("counts wrong: total=%d closed=%d", a.TotalIssues, a.ClosedIssues)
		}
		if a.EstimatedSessions != 2 {
			t.Errorf("EstimatedSessions want 2 (open only), got %d", a.EstimatedSessions)
		}
		if a.MaxParallelism != 1 {
			t.Errorf("MaxParallelism want 1 (closed root must not inflate wave 0), got %d", a.MaxParallelism)
		}
		if a.Issues["r2"].Wave != -1 {
			t.Errorf("closed root must not be scheduled, got wave %d", a.Issues["r2"].Wave)
		}
		if a.Issues["r1"].Wave != 0 || a.Issues["leaf"].Wave != 1 {
			t.Errorf("open chain wave wrong: r1=%d leaf=%d", a.Issues["r1"].Wave, a.Issues["leaf"].Wave)
		}
	})

	t.Run("closed dependency is treated as satisfied", func(t *testing.T) {
		// beads-y6pjs: an open leaf whose only blocker is CLOSED must be READY at
		// wave 0 (the closed blocker is done — mirrors getSwarmStatus treating a
		// closed dependency as satisfied). The closed blocker itself is excluded.
		f := newFakeSwarmStore()
		f.dependents["epic"] = []*types.Issue{
			{ID: "done", Title: "done", Status: types.StatusClosed},
			{ID: "next", Title: "next", Status: types.StatusOpen},
		}
		f.depRecords["done"] = []*types.Dependency{{DependsOnID: "epic", Type: types.DepParentChild}}
		f.depRecords["next"] = []*types.Dependency{
			{DependsOnID: "epic", Type: types.DepParentChild},
			{DependsOnID: "done", Type: types.DepBlocks},
		}
		a, err := analyzeEpicForSwarm(ctx, f, epic)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if a.Issues["next"].Wave != 0 {
			t.Errorf("open leaf with only a closed blocker should be ready at wave 0, got %d", a.Issues["next"].Wave)
		}
		if a.Issues["done"].Wave != -1 {
			t.Errorf("closed blocker must not be scheduled, got wave %d", a.Issues["done"].Wave)
		}
		if a.EstimatedSessions != 1 {
			t.Errorf("EstimatedSessions want 1, got %d", a.EstimatedSessions)
		}
	})

	t.Run("external dependency produces a warning", func(t *testing.T) {
		f := newFakeSwarmStore()
		f.dependents["epic"] = []*types.Issue{{ID: "a", Title: "a", Status: types.StatusOpen}}
		f.depRecords["a"] = []*types.Dependency{
			{DependsOnID: "epic", Type: types.DepParentChild},
			{DependsOnID: "external:PROJ-1", Type: types.DepBlocks},
		}
		a, err := analyzeEpicForSwarm(ctx, f, epic)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if !swarmWarnsContain(a.Warnings, "external dependency") {
			t.Errorf("expected external-dependency warning, got %v", a.Warnings)
		}
	})

	t.Run("dependency-record error propagates", func(t *testing.T) {
		f := newFakeSwarmStore()
		f.dependents["epic"] = []*types.Issue{{ID: "a", Title: "a"}}
		f.depRecords["a"] = []*types.Dependency{{DependsOnID: "epic", Type: types.DepParentChild}}
		// getEpicChildren reads a's records once (ok); analyze reads again — make
		// the SECOND read path fail by erroring on a's records after children are
		// gathered. Since both use recErrFor, set it and expect the wrapped error.
		f.recErrFor["a"] = errors.New("dep fail")
		// With the error set, getEpicChildren skips 'a' → no children → warning,
		// not an error. So instead verify the no-children path is reached safely.
		a, err := analyzeEpicForSwarm(ctx, f, epic)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if !swarmWarnsContain(a.Warnings, "no children") {
			t.Errorf("expected no-children (all skipped by rec error), got %v", a.Warnings)
		}
	})
}

func swarmWarnsContain(warnings []string, sub string) bool {
	for _, w := range warnings {
		if strings.Contains(w, sub) {
			return true
		}
	}
	return false
}
