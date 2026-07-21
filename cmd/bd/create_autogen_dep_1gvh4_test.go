//go:build cgo

package main

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/spf13/pflag"
	"github.com/steveyegge/beads/internal/types"
)

// beads-1gvh4: the beads-a8d14 atomic-create fix built the edge structs
// (pendingDeps) BEFORE the transaction, capturing issue.ID. For an
// auto-generated id — the common `bd create --deps X` / `--waits-for Y` case
// with NO --parent/--id — issue.ID is EMPTY until tx.CreateIssue mints it inside
// the transaction (issueops.GenerateIssueIDInTable writes it back onto the
// struct). So the edge was stored with an empty endpoint: `blocks:X` became
// "X -> ''" and a plain --deps became "'' -> X" — a durable dangling/corrupt
// edge, at RC=0. The fix builds the types.Dependency structs INSIDE the closure
// after the id exists.
//
// These teeth drive the REAL createCmd.RunE (no fault injection — this is a
// correctness check on the happy path) and assert the recorded edge references
// the minted id, never an empty string.
//
// MUTATION-VERIFY: with the a8d14-shipped code (pendingDeps built pre-tx), both
// tests FAIL — the edge endpoint is "" (empty) for the auto-generated id.

func TestCreateAutogenDepTargetNotEmpty_1gvh4(t *testing.T) {
	tmpDir := t.TempDir()
	real := newTestStore(t, filepath.Join(tmpDir, ".beads", "beads.db"))
	ctx := context.Background()

	if err := real.CreateIssue(ctx, &types.Issue{
		ID: "test-tgt", Title: "dep target", Status: types.StatusOpen,
		Priority: 1, IssueType: types.TypeTask,
	}, "test"); err != nil {
		t.Fatalf("create dep target: %v", err)
	}

	origStore, origCtx, origTM, origActive, origJSON, origRO := store, cmdCtx, testModeUseGlobals, storeActive, jsonOutput, readonlyMode
	t.Cleanup(func() {
		store, cmdCtx, testModeUseGlobals, storeActive, jsonOutput, readonlyMode = origStore, origCtx, origTM, origActive, origJSON, origRO
		if f := createCmd.Flags().Lookup("deps"); f != nil {
			if sv, ok := f.Value.(pflag.SliceValue); ok {
				_ = sv.Replace(nil)
			}
			f.Changed = false
		}
	})
	testModeUseGlobals = true
	cmdCtx = nil
	store = real
	rootCtx = ctx
	jsonOutput = false
	readonlyMode = false
	storeActive = true

	// --deps is a StringSlice: Set APPENDS, so a stale entry left by another
	// test's cleanup would bleed in. Clear it first, then set our value.
	if f := createCmd.Flags().Lookup("deps"); f != nil {
		if sv, ok := f.Value.(pflag.SliceValue); ok {
			_ = sv.Replace(nil)
		}
	}
	if err := createCmd.Flags().Set("deps", "blocks:test-tgt"); err != nil {
		t.Fatalf("set --deps: %v", err)
	}

	var runErr error
	_ = captureStdout(t, func() error {
		runErr = createCmd.RunE(createCmd, []string{"1gvh4 auto-gen child"})
		return runErr
	})
	if runErr != nil {
		t.Fatalf("auto-gen create --deps failed: %v", runErr)
	}

	// "blocks:test-tgt" is stored swapped as test-tgt -> <newID>. The edge on
	// test-tgt must point at a NON-EMPTY minted id.
	deps, err := real.GetDependencyRecords(ctx, "test-tgt")
	if err != nil {
		t.Fatalf("GetDependencyRecords: %v", err)
	}
	if len(deps) == 0 {
		t.Fatal("no dependency edge recorded on test-tgt after a successful auto-gen create --deps")
	}
	for _, d := range deps {
		if d.DependsOnID == "" {
			t.Errorf("REGRESSION (1gvh4): auto-gen create --deps recorded an edge with EMPTY DependsOnID (IssueID=%q type=%s) — issue.ID was captured before tx.CreateIssue minted it", d.IssueID, d.Type)
		}
		if d.IssueID == "" {
			t.Errorf("REGRESSION (1gvh4): auto-gen create --deps recorded an edge with EMPTY IssueID (DependsOnID=%q type=%s)", d.DependsOnID, d.Type)
		}
	}
}

func TestCreateAutogenWaitsForTargetNotEmpty_1gvh4(t *testing.T) {
	tmpDir := t.TempDir()
	real := newTestStore(t, filepath.Join(tmpDir, ".beads", "beads.db"))
	ctx := context.Background()

	if err := real.CreateIssue(ctx, &types.Issue{
		ID: "test-gate", Title: "gate target", Status: types.StatusOpen,
		Priority: 1, IssueType: types.TypeTask,
	}, "test"); err != nil {
		t.Fatalf("create gate target: %v", err)
	}

	origStore, origCtx, origTM, origActive, origJSON, origRO := store, cmdCtx, testModeUseGlobals, storeActive, jsonOutput, readonlyMode
	t.Cleanup(func() {
		store, cmdCtx, testModeUseGlobals, storeActive, jsonOutput, readonlyMode = origStore, origCtx, origTM, origActive, origJSON, origRO
		if f := createCmd.Flags().Lookup("waits-for"); f != nil {
			_ = f.Value.Set("")
			f.Changed = false
		}
	})
	testModeUseGlobals = true
	cmdCtx = nil
	store = real
	rootCtx = ctx
	jsonOutput = false
	readonlyMode = false
	storeActive = true

	if err := createCmd.Flags().Set("waits-for", "test-gate"); err != nil {
		t.Fatalf("set --waits-for: %v", err)
	}

	const waiterTitle = "1gvh4 auto-gen waiter"
	var runErr error
	_ = captureStdout(t, func() error {
		runErr = createCmd.RunE(createCmd, []string{waiterTitle})
		return runErr
	})
	if runErr != nil {
		t.Fatalf("auto-gen create --waits-for failed: %v", runErr)
	}

	// --waits-for is stored as <newID> waits-for test-gate, i.e. the edge lives
	// on the NEW issue's records (IssueID = the minted id). Find the created
	// issue, then assert it has a non-empty waits-for edge to test-gate. With the
	// pre-fix code the edge's IssueID was the EMPTY captured id, so the new issue
	// would have NO waits-for record at all.
	found, err := real.SearchIssues(ctx, waiterTitle, types.IssueFilter{})
	if err != nil {
		t.Fatalf("SearchIssues: %v", err)
	}
	var newID string
	for _, iss := range found {
		if iss.Title == waiterTitle {
			newID = iss.ID
			break
		}
	}
	if newID == "" {
		t.Fatalf("created waiter issue %q not found", waiterTitle)
	}

	deps, err := real.GetDependencyRecords(ctx, newID)
	if err != nil {
		t.Fatalf("GetDependencyRecords: %v", err)
	}
	var waitsForEdge *types.Dependency
	for _, d := range deps {
		if d.Type == types.DepWaitsFor {
			waitsForEdge = d
			break
		}
	}
	if waitsForEdge == nil {
		t.Fatalf("REGRESSION (1gvh4): the created issue %s has NO waits-for edge — with the empty captured id, the edge was written under IssueID='' instead of %s", newID, newID)
	}
	if waitsForEdge.DependsOnID != "test-gate" {
		t.Errorf("waits-for edge target = %q, want test-gate", waitsForEdge.DependsOnID)
	}
}
