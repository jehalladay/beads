//go:build cgo

package embeddeddolt_test

import (
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestCountsExcludeParentChild is the regression guard for beads-1d3i: the
// Count* methods that feed the `bd show --json` scalars
// (dependency_count/dependent_count) must count ONLY blocks-type edges, matching
// the list/ready path (GetDependencyCountsInTx filters `type = 'blocks'`).
//
// Before the fix, CountDependencies/CountDependents queried every edge with no
// type filter, so a parent-child hierarchy link inflated the scalar: `bd show
// Z --json` reported dependency_count:1 for a child with only a parent-child
// link while `bd list`/`bd ready` reported 0 for the same issue — the same
// field meaning "all edges" in show but "blockers only" in list/ready. A
// parent-child link is not a dependency in the blocker/readiness sense.
func TestCountsExcludeParentChild(t *testing.T) {
	skipUnlessEmbeddedDolt(t)

	te := newTestEnv(t, "cb")
	ctx := t.Context()

	// cb-epic --parent--> cb-child   (parent-child link; NOT a blocker)
	// cb-blocker --blocks--> cb-child (a real blocker of cb-child)
	// cb-child   --blocks--> cb-goal  (cb-child blocks cb-goal)
	for _, issue := range []*types.Issue{
		{ID: "cb-epic", Title: "epic", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeEpic},
		{ID: "cb-child", Title: "child", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask},
		{ID: "cb-blocker", Title: "blocker", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask},
		{ID: "cb-goal", Title: "goal", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask},
	} {
		if err := te.store.CreateIssue(ctx, issue, "tester"); err != nil {
			t.Fatalf("CreateIssue %s: %v", issue.ID, err)
		}
	}
	for _, dep := range []*types.Dependency{
		{IssueID: "cb-child", DependsOnID: "cb-epic", Type: types.DepParentChild},
		{IssueID: "cb-child", DependsOnID: "cb-blocker", Type: types.DepBlocks},
		{IssueID: "cb-goal", DependsOnID: "cb-child", Type: types.DepBlocks},
	} {
		if err := te.store.AddDependency(ctx, dep, "tester"); err != nil {
			t.Fatalf("AddDependency %s->%s: %v", dep.IssueID, dep.DependsOnID, err)
		}
	}

	// cb-child depends on cb-epic (parent-child) + cb-blocker (blocks).
	// dependency_count must count the blocker ONLY, not the parent-child link.
	if n, err := te.store.CountDependencies(ctx, "cb-child"); err != nil {
		t.Fatalf("CountDependencies: %v", err)
	} else if n != 1 {
		t.Errorf("CountDependencies(cb-child) = %d, want 1 (blocks-only; the parent-child link to cb-epic must NOT count) — matches list/ready GetDependencyCountsInTx", n)
	}

	// cb-child is depended on by cb-goal (blocks) + is the child of cb-epic
	// (parent-child, i.e. cb-epic is a dependent edge on cb-child). dependent_count
	// must count the blocker ONLY.
	if n, err := te.store.CountDependents(ctx, "cb-child"); err != nil {
		t.Fatalf("CountDependents: %v", err)
	} else if n != 1 {
		t.Errorf("CountDependents(cb-child) = %d, want 1 (blocks-only; the parent-child link from cb-epic must NOT count)", n)
	}

	// A pure parent (cb-epic) has only the parent-child edge to its child, so
	// both scalars are 0 — a hierarchy link is not a blocker in either direction.
	if n, err := te.store.CountDependents(ctx, "cb-epic"); err != nil {
		t.Fatalf("CountDependents(cb-epic): %v", err)
	} else if n != 0 {
		t.Errorf("CountDependents(cb-epic) = %d, want 0 (only a parent-child link exists)", n)
	}
	if n, err := te.store.CountDependencies(ctx, "cb-epic"); err != nil {
		t.Fatalf("CountDependencies(cb-epic): %v", err)
	} else if n != 0 {
		t.Errorf("CountDependencies(cb-epic) = %d, want 0", n)
	}
}
