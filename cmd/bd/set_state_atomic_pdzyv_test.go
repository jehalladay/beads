//go:build cgo

package main

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
)

// beads-pdzyv: the DIRECT `bd set-state` path previously performed its
// create-event + parent-child link + label-churn write sequence as SEPARATE
// calls with NO enclosing transaction, while the atomic PROXIED twin
// (runSetStateProxiedServer) buffers the same writes on one UnitOfWork and
// commits once. store.CreateIssue autocommits the event bead internally
// (GH#2009), while the link/label writes only touch the working set. A hard
// failure on AddLabel (the label write is a hard error, unlike the warned
// AddDependency) — or a crash before the deferred maybeAutoCommit flush — left
// the event durably committed but unlinked and unlabeled: an orphan invisible
// to `bd state list` (label-driven) and to child traversal, with each retry
// minting a fresh childID (duplicate-on-retry).
//
// The fix wraps the create-event + link + label churn in store.RunInTransaction
// (mirroring the proxied twin + graph_apply.go / swarm-ary2n precedent) so the
// sequence is all-or-nothing.
//
// These teeth drive the REAL setStateCmd.RunE in-process (the gate_test.go /
// swarm-ary2n save/restore-globals pattern) against a fault-injecting store
// whose AddLabel fails, and assert NO orphaned event survives + a retry does
// not accumulate a second event.
//
// MUTATION-VERIFY: revert state.go to the pre-fix separate-call sequence (drop
// the RunInTransaction wrapper) and these tests FAIL — the orphaned event
// persists after AddLabel fails, and the retry mints a second orphan.

var errInjectedLabelFailure = errors.New("injected label failure (pdzyv test)")

// faultLabelStore wraps the real DoltStorage so that AddLabel fails, both on the
// transactional path (via the wrapped tx) and on the non-transactional pre-fix
// path (via the store method itself) — so a reverted state.go that calls
// store.AddLabel directly still hits the injected failure. That symmetry is what
// makes the mutation-verify honest.
type faultLabelStore struct {
	storage.DoltStorage
}

func (f *faultLabelStore) AddLabel(ctx context.Context, issueID, label, actor string) error {
	return errInjectedLabelFailure
}

func (f *faultLabelStore) RunInTransaction(ctx context.Context, commitMsg string, fn func(tx storage.Transaction) error) error {
	return f.DoltStorage.RunInTransaction(ctx, commitMsg, func(tx storage.Transaction) error {
		return fn(&faultLabelTx{Transaction: tx})
	})
}

type faultLabelTx struct {
	storage.Transaction
}

func (t *faultLabelTx) AddLabel(ctx context.Context, issueID, label, actor string) error {
	return errInjectedLabelFailure
}

// runSetStateWithFault sets up the real store + globals, injects the AddLabel
// fault, runs setStateCmd.RunE for the given target/spec, and returns the RunE
// error.
func runSetStateWithFault(t *testing.T, real storage.DoltStorage, target, spec string) error {
	t.Helper()
	fault := &faultLabelStore{DoltStorage: real}

	origStore := store
	origRootCtx := rootCtx
	origJSONOutput := jsonOutput
	origReadonly := readonlyMode
	origActor := actor
	origExplicit := commandDidExplicitDoltCommit
	origDidWrite := commandDidWrite.Load()
	t.Cleanup(func() {
		store = origStore
		rootCtx = origRootCtx
		jsonOutput = origJSONOutput
		readonlyMode = origReadonly
		actor = origActor
		commandDidExplicitDoltCommit = origExplicit
		commandDidWrite.Store(origDidWrite)
		_ = setStateCmd.Flags().Set("reason", "")
	})

	store = fault
	rootCtx = context.Background()
	jsonOutput = false
	readonlyMode = false
	actor = "test-actor"
	_ = setStateCmd.Flags().Set("reason", "")

	var runErr error
	_ = captureStdout(t, func() error {
		runErr = setStateCmd.RunE(setStateCmd, []string{target, spec})
		return nil
	})
	return runErr
}

// countEvents returns the number of TypeEvent issues in the store (the state-change
// event beads). The pdzyv orphan is a TypeEvent child with no label edge, invisible
// to `bd state list`, so we scan ALL issues for it rather than the target's children.
func countEvents(t *testing.T, real storage.DoltStorage) int {
	t.Helper()
	all, err := real.SearchIssues(context.Background(), "", types.IssueFilter{})
	if err != nil {
		t.Fatalf("SearchIssues: %v", err)
	}
	n := 0
	for _, iss := range all {
		if iss.IssueType == types.TypeEvent {
			n++
		}
	}
	return n
}

// TestSetStateIsAtomic_pdzyv: force the label write to fail, then assert no
// orphaned state-change event survives — CreateIssue(event) must roll back with it.
func TestSetStateIsAtomic_pdzyv(t *testing.T) {
	tmpDir := t.TempDir()
	real := newTestStore(t, filepath.Join(tmpDir, ".beads", "beads.db"))
	ctx := context.Background()

	if err := real.CreateIssue(ctx, &types.Issue{
		ID: "st-1", Title: "state target", Status: types.StatusOpen,
		Priority: 1, IssueType: types.TypeTask,
	}, "test"); err != nil {
		t.Fatalf("create target: %v", err)
	}

	err := runSetStateWithFault(t, real, "st-1", "patrol=muted")
	if err == nil {
		t.Fatal("expected set-state to fail on the injected AddLabel failure")
	}

	// ATOMICITY: no state-change event may have been committed. The orphan the
	// bug produces has no dimension label (that write is exactly what failed) —
	// so it is invisible to `bd state list` (label-driven). A survivor means
	// CreateIssue(event) was not rolled back.
	if n := countEvents(t, real); n != 0 {
		t.Errorf("REGRESSION (pdzyv): %d orphaned state-change event(s) committed despite the AddLabel write failing — invisible to `bd state list`, so a retry mints a fresh childID (duplicate-on-retry)", n)
	}
}

// TestSetStateRetryDoesNotAccumulate_pdzyv: the duplicate-on-retry leg. Two
// failing runs must not leave two orphaned events (each retry called
// GetNextChildID and, pre-fix, committed a fresh event before AddLabel failed).
func TestSetStateRetryDoesNotAccumulate_pdzyv(t *testing.T) {
	tmpDir := t.TempDir()
	real := newTestStore(t, filepath.Join(tmpDir, ".beads", "beads.db"))
	ctx := context.Background()

	if err := real.CreateIssue(ctx, &types.Issue{
		ID: "st-2", Title: "state target", Status: types.StatusOpen,
		Priority: 1, IssueType: types.TypeTask,
	}, "test"); err != nil {
		t.Fatalf("create target: %v", err)
	}

	for i := 0; i < 3; i++ {
		if err := runSetStateWithFault(t, real, "st-2", "patrol=muted"); err == nil {
			t.Fatalf("attempt %d: expected set-state to fail on the injected AddLabel failure", i+1)
		}
	}

	if n := countEvents(t, real); n != 0 {
		t.Errorf("REGRESSION (pdzyv): %d orphaned events accumulated across 3 failing retries — the non-atomic path mints a fresh childID + commits an event on every attempt", n)
	}
}
