//go:build cgo

package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// beads-hqz5l: the beads-mtvlf swarm atomicity fix (@83984cc75) wrapped the
// wrapper-epic and swarm-molecule create+link pairs in one
// transactHonoringAutoCommit, but built the types.Dependency struct BEFORE
// tx.CreateIssue minted the auto-generated id — the exact beads-1gvh4 regression
// that hit create.go's a8d14 twin. The wrapper-epic struct literal has no
// explicit ID, so wrapperEpic.ID is EMPTY until tx.CreateIssue writes it back
// (issueops.GenerateIssueIDInTable); capturing wrapperEpic.ID for the edge's
// DependsOnID stored a parent-child edge with DependsOnID="". Likewise the
// swarm-molecule edge captured swarmMol.ID (its IssueID) while still empty.
//
// These teeth drive the REAL swarmCreateCmd.RunE on the happy path (no fault
// injection — this is a correctness check like the 1gvh4 tests) and assert the
// recorded edge references the minted id, never an empty string.
//
// MUTATION-VERIFY: revert swarm.go so the dep struct is built before
// tx.CreateIssue (the mtvlf-shipped shape), and this test FAILS — the wrapper
// parent-child edge's DependsOnID is "" (the auto-generated epic id was captured
// before it was minted).

// TestSwarmWrapperEpicEdgeNotEmpty_hqz5l wraps a single task as an epic and
// asserts the resulting parent-child edge points at the minted epic id.
func TestSwarmWrapperEpicEdgeNotEmpty_hqz5l(t *testing.T) {
	tmpDir := t.TempDir()
	real := newTestStore(t, filepath.Join(tmpDir, ".beads", "beads.db"))
	ctx := context.Background()

	// A plain task: `bd swarm <task>` auto-wraps a non-epic/non-molecule as an
	// epic, minting the epic id inside the tx — the seam under test.
	if err := real.CreateIssue(ctx, &types.Issue{
		ID: "test-task", Title: "a task to swarm", Status: types.StatusOpen,
		Priority: 2, IssueType: types.TypeTask,
	}, "test"); err != nil {
		t.Fatalf("create task: %v", err)
	}

	origStore := store
	origRootCtx := rootCtx
	origJSONOutput := jsonOutput
	origReadonly := readonlyMode
	origActor := actor
	t.Cleanup(func() {
		store = origStore
		rootCtx = origRootCtx
		jsonOutput = origJSONOutput
		readonlyMode = origReadonly
		actor = origActor
		_ = swarmCreateCmd.Flags().Set("coordinator", "")
		_ = swarmCreateCmd.Flags().Set("force", "false")
	})

	store = real
	rootCtx = context.Background()
	jsonOutput = false
	readonlyMode = false
	actor = "test-actor"

	var runErr error
	_ = captureStdout(t, func() error {
		runErr = swarmCreateCmd.RunE(swarmCreateCmd, []string{"test-task"})
		return nil
	})
	if runErr != nil {
		t.Fatalf("swarm create failed: %v", runErr)
	}

	// The auto-wrapped epic is the only "Swarm Epic:" issue.
	epics, err := real.SearchIssues(ctx, "Swarm Epic", types.IssueFilter{})
	if err != nil {
		t.Fatalf("SearchIssues: %v", err)
	}
	var epicID string
	for _, iss := range epics {
		if strings.HasPrefix(iss.Title, "Swarm Epic:") {
			epicID = iss.ID
			break
		}
	}
	if epicID == "" {
		t.Fatalf("auto-wrapped epic not found after swarm create")
	}

	// The parent-child edge lives on the wrapped task, pointing at the epic id.
	deps, err := real.GetDependencyRecords(ctx, "test-task")
	if err != nil {
		t.Fatalf("GetDependencyRecords: %v", err)
	}
	var pcEdge *types.Dependency
	for _, d := range deps {
		if d.Type == types.DepParentChild {
			pcEdge = d
			break
		}
	}
	if pcEdge == nil {
		t.Fatalf("REGRESSION (hqz5l): the wrapped task has NO parent-child edge — with the empty captured epic id, the edge's DependsOnID was written as \"\"")
	}
	if pcEdge.DependsOnID == "" {
		t.Errorf("REGRESSION (hqz5l): wrapper parent-child edge has EMPTY DependsOnID (IssueID=%q) — wrapperEpic.ID was captured before tx.CreateIssue minted it; want the minted epic id %q", pcEdge.IssueID, epicID)
	}
	if pcEdge.DependsOnID != epicID {
		t.Errorf("wrapper parent-child edge DependsOnID = %q, want minted epic id %q", pcEdge.DependsOnID, epicID)
	}
}

// TestSwarmMoleculeEdgeNotEmpty_hqz5l swarms an existing epic and asserts the
// swarm molecule's relates-to edge references the minted molecule id (IssueID),
// not an empty string.
func TestSwarmMoleculeEdgeNotEmpty_hqz5l(t *testing.T) {
	tmpDir := t.TempDir()
	real := newTestStore(t, filepath.Join(tmpDir, ".beads", "beads.db"))
	ctx := context.Background()

	// A swarmable epic with two children so analyzeEpicForSwarm marks it
	// swarmable and the molecule seam is reached.
	if err := real.CreateIssue(ctx, &types.Issue{
		ID: "test-epic", Title: "an epic to swarm", Status: types.StatusOpen,
		Priority: 2, IssueType: types.TypeEpic,
	}, "test"); err != nil {
		t.Fatalf("create epic: %v", err)
	}
	for _, cid := range []string{"test-c1", "test-c2"} {
		if err := real.CreateIssue(ctx, &types.Issue{
			ID: cid, Title: cid, Status: types.StatusOpen,
			Priority: 2, IssueType: types.TypeTask,
		}, "test"); err != nil {
			t.Fatalf("create child %s: %v", cid, err)
		}
		if err := real.AddDependency(ctx, &types.Dependency{
			IssueID: cid, DependsOnID: "test-epic", Type: types.DepParentChild, CreatedBy: "test",
		}, "test"); err != nil {
			t.Fatalf("link child %s: %v", cid, err)
		}
	}

	origStore := store
	origRootCtx := rootCtx
	origJSONOutput := jsonOutput
	origReadonly := readonlyMode
	origActor := actor
	t.Cleanup(func() {
		store = origStore
		rootCtx = origRootCtx
		jsonOutput = origJSONOutput
		readonlyMode = origReadonly
		actor = origActor
		_ = swarmCreateCmd.Flags().Set("coordinator", "")
		_ = swarmCreateCmd.Flags().Set("force", "false")
	})

	store = real
	rootCtx = context.Background()
	jsonOutput = false
	readonlyMode = false
	actor = "test-actor"

	var runErr error
	_ = captureStdout(t, func() error {
		runErr = swarmCreateCmd.RunE(swarmCreateCmd, []string{"test-epic"})
		return nil
	})
	if runErr != nil {
		t.Fatalf("swarm create on epic failed: %v", runErr)
	}

	// Find the swarm molecule (Title "Swarm: ...").
	mols, err := real.SearchIssues(ctx, "Swarm:", types.IssueFilter{})
	if err != nil {
		t.Fatalf("SearchIssues: %v", err)
	}
	var molID string
	for _, iss := range mols {
		if strings.HasPrefix(iss.Title, "Swarm:") {
			molID = iss.ID
			break
		}
	}
	if molID == "" {
		t.Fatalf("swarm molecule not found after swarm create on epic")
	}

	// The relates-to edge lives on the molecule (IssueID = minted molecule id).
	// Pre-fix, molDep.IssueID captured swarmMol.ID while empty, so the edge was
	// written under IssueID="" and the molecule has NO relates-to record.
	deps, err := real.GetDependencyRecords(ctx, molID)
	if err != nil {
		t.Fatalf("GetDependencyRecords: %v", err)
	}
	var relEdge *types.Dependency
	for _, d := range deps {
		if d.Type == types.DepRelatesTo {
			relEdge = d
			break
		}
	}
	if relEdge == nil {
		t.Fatalf("REGRESSION (hqz5l): swarm molecule %s has NO relates-to edge — with the empty captured molecule id, the edge was written under IssueID=\"\" instead of %s", molID, molID)
	}
	if relEdge.IssueID == "" {
		t.Errorf("REGRESSION (hqz5l): swarm molecule relates-to edge has EMPTY IssueID (DependsOnID=%q) — swarmMol.ID was captured before tx.CreateIssue minted it", relEdge.DependsOnID)
	}
	if relEdge.DependsOnID != "test-epic" {
		t.Errorf("swarm molecule relates-to edge DependsOnID = %q, want test-epic", relEdge.DependsOnID)
	}
}
