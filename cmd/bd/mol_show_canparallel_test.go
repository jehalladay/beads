package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestParallelInfoCanParallelEmptyIsNonNullArray_4mkg is the teeth for beads-4mkg.
//
// ParallelInfo.CanParallel is a []string json field ("can_parallel") with NO
// omitempty, alongside sibling fields blocked_by/blocks. The struct literal in
// analyzeMoleculeParallel initializes BlockedBy/Blocks to []string{} but NOT
// CanParallel — CanParallel is only appended in the parallel-group pass. So a
// step that is NOT in any parallel group (a lone/serial step) keeps a nil
// CanParallel, which marshals to `null` while its siblings correctly emit `[]`
// — the guib/036h/5fv3/jxel nil-slice asymmetry. The fix inits CanParallel:
// []string{} at the same root literal.
//
// This is pure Go (analyzeMoleculeParallel takes a MoleculeSubgraph literal,
// no store/cgo), so it runs in the pure-Go merge gate. RED proof: dropping the
// CanParallel:[]string{} init makes the serial step marshal can_parallel:null.
func TestParallelInfoCanParallelEmptyIsNonNullArray_4mkg(t *testing.T) {
	// A molecule where step2 is blocked by step1. Blocking stratifies depth:
	// root+step1 sit at depth 0, step2 at depth 1 — and step2 is the ONLY step
	// at depth 1, so the parallel-group pass (which only records can_parallel
	// for groups of >1) never appends to step2.CanParallel, leaving it nil. Its
	// json can_parallel must still be [] not null, matching blocked_by/blocks.
	root := &types.Issue{
		ID:        "mol-4mkg",
		Title:     "Sequential Molecule",
		Status:    types.StatusOpen,
		IssueType: types.TypeEpic,
	}
	step1 := &types.Issue{
		ID:        "mol-4mkg.step1",
		Title:     "Step 1",
		Status:    types.StatusOpen,
		IssueType: types.TypeTask,
	}
	step2 := &types.Issue{
		ID:        "mol-4mkg.step2",
		Title:     "Step 2 (blocked by Step 1)",
		Status:    types.StatusOpen,
		IssueType: types.TypeTask,
	}

	subgraph := &MoleculeSubgraph{
		Root:   root,
		Issues: []*types.Issue{root, step1, step2},
		IssueMap: map[string]*types.Issue{
			root.ID:  root,
			step1.ID: step1,
			step2.ID: step2,
		},
		Dependencies: []*types.Dependency{
			{IssueID: step1.ID, DependsOnID: root.ID, Type: types.DepParentChild},
			{IssueID: step2.ID, DependsOnID: root.ID, Type: types.DepParentChild},
			{IssueID: step2.ID, DependsOnID: step1.ID, Type: types.DepBlocks}, // step2 blocked by step1
		},
	}

	analysis := analyzeMoleculeParallel(subgraph, nil)

	// step2 is alone at its depth → in NO parallel group → CanParallel untouched.
	info := analysis.Steps[step2.ID]
	if info == nil {
		t.Fatalf("no ParallelInfo for %s", step2.ID)
	}
	if info.ParallelGroup != "" {
		t.Fatalf("test precondition broken: step2 unexpectedly in a parallel group %q — pick a scenario where it is alone", info.ParallelGroup)
	}
	if info.CanParallel == nil {
		t.Errorf("Step1.CanParallel is nil — must be an initialized empty slice so it marshals to [] not null (beads-4mkg)")
	}

	// The real contract: the marshaled json must carry can_parallel:[] not null,
	// matching sibling blocked_by/blocks.
	data, err := json.Marshal(info)
	if err != nil {
		t.Fatalf("marshal ParallelInfo: %v", err)
	}
	s := string(data)
	if strings.Contains(s, `"can_parallel":null`) {
		t.Errorf("can_parallel marshaled to null for a step outside a parallel group — must be [] like sibling blocked_by/blocks (beads-4mkg)\njson: %s", s)
	}
	if !strings.Contains(s, `"can_parallel":[]`) {
		t.Errorf("expected can_parallel:[] for a lone/serial step, got: %s", s)
	}

	// Regression: a step that CAN parallelize still gets its peers listed.
	// Two sibling children of root with no cross-blocking parallelize.
	root2 := &types.Issue{ID: "mol-4mkg2", Title: "Parallel Molecule", Status: types.StatusOpen, IssueType: types.TypeEpic}
	c1 := &types.Issue{ID: "mol-4mkg2.c1", Title: "C1", Status: types.StatusOpen, IssueType: types.TypeTask}
	c2 := &types.Issue{ID: "mol-4mkg2.c2", Title: "C2", Status: types.StatusOpen, IssueType: types.TypeTask}
	sg2 := &MoleculeSubgraph{
		Root:   root2,
		Issues: []*types.Issue{root2, c1, c2},
		IssueMap: map[string]*types.Issue{
			root2.ID: root2, c1.ID: c1, c2.ID: c2,
		},
		Dependencies: []*types.Dependency{
			{IssueID: c1.ID, DependsOnID: root2.ID, Type: types.DepParentChild},
			{IssueID: c2.ID, DependsOnID: root2.ID, Type: types.DepParentChild},
		},
	}
	a2 := analyzeMoleculeParallel(sg2, nil)
	c1info := a2.Steps[c1.ID]
	if c1info == nil || len(c1info.CanParallel) == 0 {
		t.Errorf("C1.CanParallel should list its parallel peer, got %+v", c1info)
	}
}
