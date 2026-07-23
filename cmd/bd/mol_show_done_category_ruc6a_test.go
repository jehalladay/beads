package main

import (
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestAnalyzeMoleculeParallelDoneCategory_ruc6a is the teeth for beads-ruc6a.
//
// analyzeMoleculeParallel is the in-memory molecule step-readiness/blocker graph
// analyzer (cmd/bd/mol_show.go) that feeds `bd mol current`, `bd mol show
// --parallel`, `bd ready --mol` (direct + proxied) and the proxied autoclose
// readyIDs gate. It is used INSTEAD of GetReadyWork because wisp steps are
// ephemeral. Before ruc6a it keyed blocker-activeness and the waits-for gate on
// literal StatusClosed, so a step blocked only by a sibling in a CUSTOM
// done-category status (e.g. `resolved`, registered via
// `bd config set status.custom "resolved:done"`) was reported still-blocked /
// not-ready — diverging from bd ready / is_blocked / getMoleculeProgress
// (beads-x463g), which post-x463g treat a done-category target as complete.
//
// The fix threads the configured done-category status names (DONE-ONLY, matching
// x463g's resolveDoneStatusNamesInTx = CustomStatusesByCategory(CategoryDone);
// Frozen deliberately NOT included) into analyzeMoleculeParallel + its
// calculateBlockingDepths helper so a done-category blocker/child counts complete
// exactly like a literal close.
//
// Pure Go (analyzeMoleculeParallel takes a MoleculeSubgraph literal + a done-set
// map, no store/cgo), so it runs in the pure-Go merge gate. RED proof: with the
// done arg dropped (nil), the done-category blocker stays active and step2 is not
// ready; GREEN with the done-set supplied.
func TestAnalyzeMoleculeParallelDoneCategory_ruc6a(t *testing.T) {
	// A custom done-category status name (as `bd config set status.custom
	// "resolved:done"` would register). done-set maps this name -> true.
	const doneStatus = types.Status("resolved")
	done := map[string]bool{string(doneStatus): true}

	t.Run("blocks_edge_done_category_blocker_unblocks_dependent", func(t *testing.T) {
		root := &types.Issue{ID: "mol-ruc6a", Title: "Molecule", Status: types.StatusOpen, IssueType: types.TypeEpic}
		// step1 sits in a CUSTOM done-category status (not literal closed).
		step1 := &types.Issue{ID: "mol-ruc6a.step1", Title: "Step 1 (done-category)", Status: doneStatus, IssueType: types.TypeTask}
		step2 := &types.Issue{ID: "mol-ruc6a.step2", Title: "Step 2 (blocked by Step 1)", Status: types.StatusOpen, IssueType: types.TypeTask}

		subgraph := &MoleculeSubgraph{
			Root:   root,
			Issues: []*types.Issue{root, step1, step2},
			IssueMap: map[string]*types.Issue{
				root.ID: root, step1.ID: step1, step2.ID: step2,
			},
			Dependencies: []*types.Dependency{
				{IssueID: step1.ID, DependsOnID: root.ID, Type: types.DepParentChild},
				{IssueID: step2.ID, DependsOnID: root.ID, Type: types.DepParentChild},
				{IssueID: step2.ID, DependsOnID: step1.ID, Type: types.DepBlocks}, // step2 blocked by step1
			},
		}

		analysis := analyzeMoleculeParallel(subgraph, done)
		info := analysis.Steps[step2.ID]
		if info == nil {
			t.Fatalf("no ParallelInfo for %s", step2.ID)
		}
		// step1 is done-category → it must NOT appear as an active blocker of step2.
		if len(info.BlockedBy) != 0 {
			t.Errorf("step2.BlockedBy = %v; a done-category blocker (step1=%q) must not count as active (beads-ruc6a)", info.BlockedBy, doneStatus)
		}
		// With no active blocker, an open step is ready.
		if !info.IsReady {
			t.Errorf("step2.IsReady = false; must be ready once its only blocker is in a done-category status (beads-ruc6a)")
		}
	})

	t.Run("waits_for_all_children_done_category_child_satisfies_gate", func(t *testing.T) {
		// spawner waits-for all children of the parent; the single child is in a
		// custom done-category status. The gate must be satisfied (child counts
		// complete) so the spawner is not blocked.
		root := &types.Issue{ID: "mol-ruc6a2", Title: "Molecule", Status: types.StatusOpen, IssueType: types.TypeEpic}
		parent := &types.Issue{ID: "mol-ruc6a2.parent", Title: "Parent", Status: types.StatusOpen, IssueType: types.TypeTask}
		child := &types.Issue{ID: "mol-ruc6a2.child", Title: "Child (done-category)", Status: doneStatus, IssueType: types.TypeTask}
		spawner := &types.Issue{ID: "mol-ruc6a2.spawner", Title: "Spawner (waits-for parent)", Status: types.StatusOpen, IssueType: types.TypeTask}

		subgraph := &MoleculeSubgraph{
			Root:   root,
			Issues: []*types.Issue{root, parent, child, spawner},
			IssueMap: map[string]*types.Issue{
				root.ID: root, parent.ID: parent, child.ID: child, spawner.ID: spawner,
			},
			Dependencies: []*types.Dependency{
				{IssueID: parent.ID, DependsOnID: root.ID, Type: types.DepParentChild},
				{IssueID: spawner.ID, DependsOnID: root.ID, Type: types.DepParentChild},
				{IssueID: child.ID, DependsOnID: parent.ID, Type: types.DepParentChild},
				// spawner waits for parent's children (all-children gate).
				{IssueID: spawner.ID, DependsOnID: parent.ID, Type: types.DepWaitsFor},
			},
		}

		analysis := analyzeMoleculeParallel(subgraph, done)
		info := analysis.Steps[spawner.ID]
		if info == nil {
			t.Fatalf("no ParallelInfo for %s", spawner.ID)
		}
		if len(info.BlockedBy) != 0 {
			t.Errorf("spawner.BlockedBy = %v; a done-category child must satisfy the waits-for gate like a closed child (beads-ruc6a)", info.BlockedBy)
		}
		if !info.IsReady {
			t.Errorf("spawner.IsReady = false; the waits-for gate must be satisfied by a done-category child (beads-ruc6a)")
		}
	})

	// Negative: a still-OPEN sibling blocker must remain active (no over-clearing).
	t.Run("open_blocker_still_blocks", func(t *testing.T) {
		root := &types.Issue{ID: "mol-ruc6a3", Title: "Molecule", Status: types.StatusOpen, IssueType: types.TypeEpic}
		step1 := &types.Issue{ID: "mol-ruc6a3.step1", Title: "Step 1 (open)", Status: types.StatusOpen, IssueType: types.TypeTask}
		step2 := &types.Issue{ID: "mol-ruc6a3.step2", Title: "Step 2 (blocked)", Status: types.StatusOpen, IssueType: types.TypeTask}

		subgraph := &MoleculeSubgraph{
			Root:   root,
			Issues: []*types.Issue{root, step1, step2},
			IssueMap: map[string]*types.Issue{
				root.ID: root, step1.ID: step1, step2.ID: step2,
			},
			Dependencies: []*types.Dependency{
				{IssueID: step1.ID, DependsOnID: root.ID, Type: types.DepParentChild},
				{IssueID: step2.ID, DependsOnID: root.ID, Type: types.DepParentChild},
				{IssueID: step2.ID, DependsOnID: step1.ID, Type: types.DepBlocks},
			},
		}

		analysis := analyzeMoleculeParallel(subgraph, done)
		info := analysis.Steps[step2.ID]
		if info == nil {
			t.Fatalf("no ParallelInfo for %s", step2.ID)
		}
		if len(info.BlockedBy) != 1 || info.BlockedBy[0] != step1.ID {
			t.Errorf("step2.BlockedBy = %v; an OPEN blocker must still be active (beads-ruc6a negative)", info.BlockedBy)
		}
		if info.IsReady {
			t.Errorf("step2.IsReady = true; must stay blocked by an open sibling (beads-ruc6a negative)")
		}
	})

	// Negative: a FROZEN-category status must NOT unblock (parked != done, matching
	// x463g's DONE-only blocker semantics). Here the done-set carries only the
	// done-category name; a frozen status is absent from it, so the blocker stays
	// active — proving the fix keys on the supplied done-set, not "any custom
	// status".
	t.Run("frozen_category_blocker_still_blocks", func(t *testing.T) {
		const frozenStatus = types.Status("parked")
		root := &types.Issue{ID: "mol-ruc6a4", Title: "Molecule", Status: types.StatusOpen, IssueType: types.TypeEpic}
		step1 := &types.Issue{ID: "mol-ruc6a4.step1", Title: "Step 1 (frozen-category)", Status: frozenStatus, IssueType: types.TypeTask}
		step2 := &types.Issue{ID: "mol-ruc6a4.step2", Title: "Step 2 (blocked)", Status: types.StatusOpen, IssueType: types.TypeTask}

		subgraph := &MoleculeSubgraph{
			Root:   root,
			Issues: []*types.Issue{root, step1, step2},
			IssueMap: map[string]*types.Issue{
				root.ID: root, step1.ID: step1, step2.ID: step2,
			},
			Dependencies: []*types.Dependency{
				{IssueID: step1.ID, DependsOnID: root.ID, Type: types.DepParentChild},
				{IssueID: step2.ID, DependsOnID: root.ID, Type: types.DepParentChild},
				{IssueID: step2.ID, DependsOnID: step1.ID, Type: types.DepBlocks},
			},
		}

		// done-set carries ONLY the done-category name (not the frozen one).
		analysis := analyzeMoleculeParallel(subgraph, done)
		info := analysis.Steps[step2.ID]
		if info == nil {
			t.Fatalf("no ParallelInfo for %s", step2.ID)
		}
		if len(info.BlockedBy) != 1 || info.BlockedBy[0] != step1.ID {
			t.Errorf("step2.BlockedBy = %v; a FROZEN-category blocker must still be active (parked != done, beads-ruc6a negative)", info.BlockedBy)
		}
		if info.IsReady {
			t.Errorf("step2.IsReady = true; a frozen-category blocker must not unblock (beads-ruc6a negative)")
		}
	})
}
