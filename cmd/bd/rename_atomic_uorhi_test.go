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

// beads-uorhi: the DIRECT `bd rename` path renamed the issue id
// (store.UpdateIssueID — its OWN self-committing withRetryTx) and THEN rewrote
// every cross-issue reference in a SEPARATE self-committing loop. The atomic
// PROXIED twin (runRenameProxiedServer) instead stages the rename + all ref
// rewrites onto ONE UnitOfWork and commits once. So a mid-sweep failure (a
// per-issue UpdateIssue error, process death, or ctx cancel) left the id
// already renamed while an arbitrary SUFFIX of the issue set still textually
// referenced the now-nonexistent old id — durable dangling refs — and the
// caller downgraded that to a soft warning with RC=0, so the operator believed
// the rename fully succeeded.
//
// The fix wraps store.UpdateIssueID + updateReferencesInAllIssuesTx in ONE
// store.RunInTransaction (mirroring the proxied twin + the ary2n/zcq86/pdzyv
// direct-leg-to-in-tx precedent). All-or-nothing: a ref-rewrite failure rolls
// the rename back, so the OLD id keeps resolving and no dangling ref survives.
//
// These teeth drive the REAL renameCmd.RunE in-process (the gate_test.go /
// pdzyv save/restore-globals pattern) against a fault store whose UpdateIssue
// fails when rewriting a referencing issue, then assert the rename was rolled
// back atomically.
//
// MUTATION-VERIFY: revert rename.go to the pre-fix two-step (self-committing
// store.UpdateIssueID then a best-effort ref loop that returns RC=0 on failure)
// and TestRenameIsAtomic_uorhi FAILS — the old id no longer resolves (renamed
// and committed) while the referencing issue still points at it (dangling), and
// runRename returns nil (RC=0) instead of surfacing the error.

var errInjectedRefRewriteFailure = errors.New("injected reference-rewrite failure (uorhi test)")

// faultRefRewriteStore wraps the real DoltStorage so that UpdateIssue fails for
// a designated issue id — the referencing issue whose text ref the rename sweep
// tries to rewrite. It faults both on the transactional path (via the wrapped
// tx) and on the non-transactional store method, so a reverted rename.go that
// calls store.UpdateIssue directly in a best-effort loop still hits the same
// injected failure. That symmetry makes the mutation-verify honest.
type faultRefRewriteStore struct {
	storage.DoltStorage
	failIssueID string
}

func (f *faultRefRewriteStore) UpdateIssue(ctx context.Context, id string, updates map[string]interface{}, actor string) error {
	if id == f.failIssueID {
		return errInjectedRefRewriteFailure
	}
	return f.DoltStorage.UpdateIssue(ctx, id, updates, actor)
}

func (f *faultRefRewriteStore) RunInTransaction(ctx context.Context, commitMsg string, fn func(tx storage.Transaction) error) error {
	return f.DoltStorage.RunInTransaction(ctx, commitMsg, func(tx storage.Transaction) error {
		return fn(&faultRefRewriteTx{Transaction: tx, failIssueID: f.failIssueID})
	})
}

type faultRefRewriteTx struct {
	storage.Transaction
	failIssueID string
}

func (t *faultRefRewriteTx) UpdateIssue(ctx context.Context, id string, updates map[string]interface{}, actor string) error {
	if id == t.failIssueID {
		return errInjectedRefRewriteFailure
	}
	return t.Transaction.UpdateIssue(ctx, id, updates, actor)
}

// runRenameWithFault sets up the real store + globals, injects the UpdateIssue
// fault for failIssueID, runs renameCmd.RunE, and returns the RunE error.
func runRenameWithFault(t *testing.T, real storage.DoltStorage, oldID, newID, failIssueID string) error {
	t.Helper()
	fault := &faultRefRewriteStore{DoltStorage: real, failIssueID: failIssueID}

	origStore := store
	origRootCtx := rootCtx
	origJSONOutput := jsonOutput
	origReadonly := readonlyMode
	origActive := storeActive
	origDidWrite := commandDidWrite.Load()
	t.Cleanup(func() {
		store = origStore
		rootCtx = origRootCtx
		jsonOutput = origJSONOutput
		readonlyMode = origReadonly
		storeActive = origActive
		commandDidWrite.Store(origDidWrite)
	})

	store = fault
	rootCtx = context.Background()
	jsonOutput = false
	readonlyMode = false
	// Mark the injected store active so runRename's ensureStoreActive() does not
	// re-open a real store from metadata and discard our fault wrapper.
	storeActive = true

	var runErr error
	_ = captureStdout(t, func() error {
		runErr = renameCmd.RunE(renameCmd, []string{oldID, newID})
		return nil
	})
	return runErr
}

// TestRenameIsAtomic_uorhi: force the reference-rewrite of a referencing issue
// to fail mid-sweep, then assert the rename rolled back — the OLD id still
// resolves, the NEW id does not, no issue is left pointing at the vanished id,
// and runRename surfaced the error (not RC=0).
func TestRenameIsAtomic_uorhi(t *testing.T) {
	tmpDir := t.TempDir()
	real := newTestStore(t, filepath.Join(tmpDir, ".beads", "beads.db"))
	ctx := context.Background()

	// The issue to rename.
	if err := real.CreateIssue(ctx, &types.Issue{
		ID: "test-abc", Title: "target", Status: types.StatusOpen,
		Priority: 1, IssueType: types.TypeTask,
	}, "test"); err != nil {
		t.Fatalf("create target: %v", err)
	}
	// An issue whose description references the target; its UpdateIssue is the
	// one we fault, standing in for any mid-sweep ref-rewrite failure.
	if err := real.CreateIssue(ctx, &types.Issue{
		ID: "test-ref", Title: "refs target", Description: "depends on test-abc for context",
		Status: types.StatusOpen, Priority: 1, IssueType: types.TypeTask,
	}, "test"); err != nil {
		t.Fatalf("create ref: %v", err)
	}

	err := runRenameWithFault(t, real, "test-abc", "test-xyz", "test-ref")
	if err == nil {
		t.Fatal("expected rename to fail on the injected reference-rewrite failure, got nil (RC=0) [beads-uorhi]")
	}

	// ATOMICITY: the id rename must have rolled back with the failed ref rewrite.
	// The OLD id must still resolve.
	if _, gerr := real.GetIssue(ctx, "test-abc"); gerr != nil {
		t.Errorf("REGRESSION (uorhi): old id test-abc no longer resolves after a FAILED rename — the id rename committed before the ref-rewrite loop failed, so it was not rolled back: %v", gerr)
	}
	// The NEW id must NOT exist.
	if _, gerr := real.GetIssue(ctx, "test-xyz"); gerr == nil {
		t.Errorf("REGRESSION (uorhi): new id test-xyz exists after a FAILED rename — the rename was not rolled back atomically")
	}
	// No issue may still textually reference the (would-be vanished) old id via a
	// half-applied sweep. With rollback, test-ref's description is unchanged and
	// still references test-abc, which correctly STILL exists — so there is no
	// dangling ref. The dangling-ref failure mode is: old id gone but a ref to it
	// remains. We already asserted test-abc still resolves, so any remaining
	// reference to it is valid, not dangling.
	refIssue, gerr := real.GetIssue(ctx, "test-ref")
	if gerr != nil {
		t.Fatalf("get ref issue: %v", gerr)
	}
	if refIssue.Description != "depends on test-abc for context" {
		t.Errorf("REGRESSION (uorhi): referencing issue description was mutated on a FAILED rename: %q — the sweep half-applied instead of rolling back", refIssue.Description)
	}
}

// TestRenameHappyPathStillRewrites_uorhi guards that wrapping the rename in a
// transaction did not break the normal case: a successful rename still renames
// the id AND rewrites references across other issues (the g8qfo/1nvr5 behavior),
// all in the one commit.
func TestRenameHappyPathStillRewrites_uorhi(t *testing.T) {
	tmpDir := t.TempDir()
	real := newTestStore(t, filepath.Join(tmpDir, ".beads", "beads.db"))
	ctx := context.Background()

	if err := real.CreateIssue(ctx, &types.Issue{
		ID: "test-abc", Title: "target", Status: types.StatusOpen,
		Priority: 1, IssueType: types.TypeTask,
	}, "test"); err != nil {
		t.Fatalf("create target: %v", err)
	}
	if err := real.CreateIssue(ctx, &types.Issue{
		ID: "test-ref", Title: "refs target", Description: "see test-abc and sibling test-abc-2",
		Status: types.StatusOpen, Priority: 1, IssueType: types.TypeTask,
	}, "test"); err != nil {
		t.Fatalf("create ref: %v", err)
	}

	// No fault: rename against the real store (empty failIssueID never matches).
	if err := runRenameWithFault(t, real, "test-abc", "test-xyz", ""); err != nil {
		t.Fatalf("happy-path rename failed: %v", err)
	}

	if _, gerr := real.GetIssue(ctx, "test-xyz"); gerr != nil {
		t.Errorf("happy-path: renamed id test-xyz does not resolve: %v", gerr)
	}
	if _, gerr := real.GetIssue(ctx, "test-abc"); gerr == nil {
		t.Errorf("happy-path: old id test-abc still resolves after a successful rename")
	}
	refIssue, gerr := real.GetIssue(ctx, "test-ref")
	if gerr != nil {
		t.Fatalf("get ref issue: %v", gerr)
	}
	// The standalone ref is rewritten; the hyphen-extended sibling id is preserved
	// (1nvr5 token boundary), all committed with the rename.
	if want := "see test-xyz and sibling test-abc-2"; refIssue.Description != want {
		t.Errorf("happy-path reference rewrite wrong:\n got %q\nwant %q [beads-uorhi/g8qfo/1nvr5]", refIssue.Description, want)
	}
}
