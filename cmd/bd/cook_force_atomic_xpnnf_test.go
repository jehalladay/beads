//go:build cgo

package main

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/steveyegge/beads/internal/formula"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
)

// beads-xpnnf: `cook --persist --force` replaces an existing proto by
// delete-then-recreate. persistCookFormula previously ran deleteProtoSubgraph
// and cookFormula as TWO independent transactions — the delete COMMITTED first,
// so a DB-level fault inside the recreate (CreateIssues/label/dep writes) left
// the old proto subgraph permanently GONE with nothing recreated (a destructive
// delete not rolled back on recreate failure). The fix runs the delete +
// recreate in ONE outer transaction (the ary2n/a8d14/uorhi MULTI-WRITE-ATOMICITY
// pattern), so a recreate failure rolls the delete back and the old proto
// survives.
//
// These teeth build a proto, then drive persistCookFormula(--force) against a
// fault-injecting store whose recreate write (CreateIssues) fails, and assert
// (a) persistCookFormula returns an error and (b) the ORIGINAL proto subgraph is
// still present (the destructive delete was rolled back).
//
// MUTATION-VERIFY: revert persistCookFormula to the two-separate-transactions
// shape (deleteProtoSubgraph then cookFormula) and this test FAILS — the delete
// commits, the recreate faults, and the old proto is gone.

type faultCookStore struct {
	storage.DoltStorage
	failCreateIssues bool
}

var errInjectedCookFailure = errors.New("injected recreate failure (xpnnf test)")

func (f *faultCookStore) RunInTransaction(ctx context.Context, commitMsg string, fn func(tx storage.Transaction) error) error {
	return f.DoltStorage.RunInTransaction(ctx, commitMsg, func(tx storage.Transaction) error {
		return fn(&faultCookTx{Transaction: tx, parent: f})
	})
}

type faultCookTx struct {
	storage.Transaction
	parent *faultCookStore
}

// CreateIssues faults on the recreate write (which runs AFTER the delete inside
// the same outer tx), standing in for a mid-recreate DB fault. It only fires
// while failCreateIssues is armed so the initial proto build succeeds.
func (t *faultCookTx) CreateIssues(ctx context.Context, issues []*types.Issue, actor string) error {
	if t.parent.failCreateIssues {
		return errInjectedCookFailure
	}
	return t.Transaction.CreateIssues(ctx, issues, actor)
}

func testCookFormula() *formula.Formula {
	return &formula.Formula{
		Formula:     "xpnnf-fixture",
		Description: "atomic recook fixture",
		Steps: []*formula.Step{
			{ID: "s1", Title: "step one", Type: "task"},
			{ID: "s2", Title: "step two", Type: "task", DependsOn: []string{"s1"}},
		},
	}
}

func TestCookForceReplaceIsAtomic_xpnnf(t *testing.T) {
	tmpDir := t.TempDir()
	real := newTestStore(t, filepath.Join(tmpDir, ".beads", "beads.db"))
	ctx := context.Background()

	const protoID = "mol-xpnnf-proto"

	origStore := store
	origRootCtx := rootCtx
	origJSONOutput := jsonOutput
	origActor := actor
	t.Cleanup(func() {
		store = origStore
		rootCtx = origRootCtx
		jsonOutput = origJSONOutput
		actor = origActor
	})

	store = real
	rootCtx = ctx
	jsonOutput = true
	actor = "test-actor"

	// 1. Build the initial proto (no fault) so a --force replace has something
	//    to delete.
	if err := persistCookFormula(ctx, testCookFormula(), protoID, false, nil, nil); err != nil {
		t.Fatalf("initial cook failed: %v", err)
	}
	// Sanity: the proto subgraph exists and has the expected node count.
	before, err := loadTemplateSubgraph(ctx, real, protoID)
	if err != nil {
		t.Fatalf("load proto after initial cook: %v", err)
	}
	wantIssues := len(before.Issues)
	if wantIssues < 3 { // root + 2 steps
		t.Fatalf("expected >=3 issues in the initial proto, got %d", wantIssues)
	}

	// 2. Re-cook with --force through a store whose recreate CreateIssues faults
	//    AFTER the delete. The whole delete+recreate must roll back.
	fault := &faultCookStore{DoltStorage: real, failCreateIssues: true}
	store = fault
	err = persistCookFormula(ctx, testCookFormula(), protoID, true, nil, nil)
	if err == nil {
		t.Fatalf("expected a non-nil error when the recreate write fails; got nil (pre-fix bug commits the delete then drops the recreate)")
	}

	// 3. The original proto subgraph must survive — the destructive delete was
	//    rolled back with the failed recreate.
	store = real
	after, err := loadTemplateSubgraph(ctx, real, protoID)
	if err != nil {
		t.Fatalf("REGRESSION (xpnnf): loading the proto after a failed --force recook errored — the old proto was destroyed and not rolled back: %v", err)
	}
	if len(after.Issues) != wantIssues {
		t.Errorf("REGRESSION (xpnnf): proto subgraph has %d issues after a failed --force recook, want %d — the destructive delete committed despite the recreate failing (non-atomic delete-then-recreate)", len(after.Issues), wantIssues)
	}
}
