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

// beads-tvinu: the DIRECT `bd gate create` path previously performed its
// create-gate + blocking-dependency write sequence as SEPARATE calls with NO
// enclosing transaction (store.CreateIssue → store.AddDependency → store.Commit),
// while the atomic PROXIED twin (runGateCreateProxied) buffers CreateIssue +
// AddDependency on one UnitOfWork and commits once. store.CreateIssue autocommits
// the gate issue internally (GH#2009) while AddDependency only touches the working
// set until the explicit store.Commit — so a hard failure on AddDependency (or a
// crash before the Commit) left the gate DURABLY created but not blocking its
// target: an orphan gate that gates nothing, while the target issue stays out of
// `bd ready` on no dependency at all (defeating the entire purpose of the verb).
// Exact ary2n/pdzyv atomicity signature.
//
// The fix wraps the create-gate + add-dependency sequence in store.RunInTransaction
// (mirroring the proxied twin + pdzyv/graph_apply precedent) so it is
// all-or-nothing.
//
// These teeth drive the REAL gateCreateCmd.RunE in-process (the
// set_state_atomic_pdzyv save/restore-globals pattern) against a fault-injecting
// store whose AddDependency fails, and assert NO orphaned gate survives + a retry
// does not accumulate a second gate.
//
// MUTATION-VERIFY: revert gate.go to the pre-fix separate-call sequence (drop the
// RunInTransaction wrapper, restore store.CreateIssue + store.AddDependency +
// store.Commit) and these tests FAIL — the orphaned gate persists after
// AddDependency fails, and each retry mints a fresh gate.

var errInjectedDepFailure = errors.New("injected dependency failure (tvinu test)")

// faultDepStore wraps the real DoltStorage so AddDependency fails, both on the
// transactional path (via the wrapped tx) and on the non-transactional pre-fix
// path (via the store method itself) — so a reverted gate.go that calls
// store.AddDependency directly still hits the injected failure. That symmetry is
// what makes the mutation-verify honest.
type faultDepStore struct {
	storage.DoltStorage
}

func (f *faultDepStore) AddDependency(ctx context.Context, dep *types.Dependency, actor string) error {
	return errInjectedDepFailure
}

func (f *faultDepStore) RunInTransaction(ctx context.Context, commitMsg string, fn func(tx storage.Transaction) error) error {
	return f.DoltStorage.RunInTransaction(ctx, commitMsg, func(tx storage.Transaction) error {
		return fn(&faultDepTx{Transaction: tx})
	})
}

type faultDepTx struct {
	storage.Transaction
}

func (t *faultDepTx) AddDependency(ctx context.Context, dep *types.Dependency, actor string) error {
	return errInjectedDepFailure
}

// runGateCreateWithFault sets up the real store + globals, injects the
// AddDependency fault, runs gateCreateCmd.RunE blocking the given target, and
// returns the RunE error.
func runGateCreateWithFault(t *testing.T, real storage.DoltStorage, blocksID string) error {
	t.Helper()
	fault := &faultDepStore{DoltStorage: real}

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
		_ = gateCreateCmd.Flags().Set("blocks", "")
		_ = gateCreateCmd.Flags().Set("type", "human")
		_ = gateCreateCmd.Flags().Set("reason", "")
	})

	store = fault
	rootCtx = context.Background()
	jsonOutput = false
	readonlyMode = false
	actor = "test-actor"
	_ = gateCreateCmd.Flags().Set("blocks", blocksID)
	_ = gateCreateCmd.Flags().Set("type", "human")
	_ = gateCreateCmd.Flags().Set("reason", "")

	var runErr error
	_ = captureStdout(t, func() error {
		runErr = gateCreateCmd.RunE(gateCreateCmd, nil)
		return nil
	})
	return runErr
}

// countGates returns the number of "gate"-type issues in the store. The tvinu
// orphan is a gate issue with no blocking dependency edge, so we scan ALL issues
// for it.
func countGates(t *testing.T, real storage.DoltStorage) int {
	t.Helper()
	all, err := real.SearchIssues(context.Background(), "", types.IssueFilter{})
	if err != nil {
		t.Fatalf("SearchIssues: %v", err)
	}
	n := 0
	for _, iss := range all {
		if iss.IssueType == types.IssueType("gate") {
			n++
		}
	}
	return n
}

// TestGateCreateIsAtomic_tvinu: force the blocking-dependency write to fail, then
// assert no orphaned gate survives — CreateIssue(gate) must roll back with it.
func TestGateCreateIsAtomic_tvinu(t *testing.T) {
	tmpDir := t.TempDir()
	real := newTestStore(t, filepath.Join(tmpDir, ".beads", "beads.db"))
	ctx := context.Background()

	if err := real.CreateIssue(ctx, &types.Issue{
		ID: "gt-1", Title: "gate target", Status: types.StatusOpen,
		Priority: 1, IssueType: types.TypeTask,
	}, "test"); err != nil {
		t.Fatalf("create target: %v", err)
	}

	err := runGateCreateWithFault(t, real, "gt-1")
	if err == nil {
		t.Fatal("expected gate create to fail on the injected AddDependency failure")
	}

	// ATOMICITY: no gate issue may have been committed. The orphan the bug
	// produces has no blocking dependency (that write is exactly what failed) —
	// so it gates nothing while surviving durably. A survivor means
	// CreateIssue(gate) was not rolled back.
	if n := countGates(t, real); n != 0 {
		t.Errorf("REGRESSION (tvinu): %d orphaned gate(s) committed despite the AddDependency write failing — an orphan gate that gates nothing, and the target is not blocked at all", n)
	}
}

// TestGateCreateRetryDoesNotAccumulate_tvinu: the duplicate-on-retry leg. Three
// failing runs must not leave three orphaned gates (pre-fix, each attempt
// committed a fresh gate issue before AddDependency failed).
func TestGateCreateRetryDoesNotAccumulate_tvinu(t *testing.T) {
	tmpDir := t.TempDir()
	real := newTestStore(t, filepath.Join(tmpDir, ".beads", "beads.db"))
	ctx := context.Background()

	if err := real.CreateIssue(ctx, &types.Issue{
		ID: "gt-2", Title: "gate target", Status: types.StatusOpen,
		Priority: 1, IssueType: types.TypeTask,
	}, "test"); err != nil {
		t.Fatalf("create target: %v", err)
	}

	for i := 0; i < 3; i++ {
		if err := runGateCreateWithFault(t, real, "gt-2"); err == nil {
			t.Fatalf("attempt %d: expected gate create to fail on the injected AddDependency failure", i+1)
		}
	}

	if n := countGates(t, real); n != 0 {
		t.Errorf("REGRESSION (tvinu): %d orphaned gates accumulated across 3 failing retries — the non-atomic path commits a fresh gate on every attempt", n)
	}
}
