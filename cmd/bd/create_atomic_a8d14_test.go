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

// beads-a8d14: the DIRECT `bd create` path previously self-committed the issue
// via store.CreateIssue and THEN added each --parent / --deps / --waits-for edge
// best-effort (WarnError + RC=0 on failure). A create whose issue succeeded but
// whose edge write failed therefore left a durable issue MISSING its edges at
// exit 0 — while the atomic PROXIED twin (create_proxied_server.go) buffers the
// same writes on one UOW and commits once. The fix wraps CreateIssue + all
// AddDependency calls in one store.RunInTransaction (via
// transactHonoringAutoCommit, mirroring the proxied UOW + graph_apply.go /
// cook.go / swarm(ary2n) precedents) so an edge failure rolls the issue back too
// and the command exits non-zero — restoring parity with the proxied path.
//
// These teeth drive the REAL createCmd.RunE in-process (the create_json_error_test
// save/restore-globals pattern) against a fault-injecting store whose Nth
// AddDependency fails, and assert (a) RunE returns an error and (b) NO orphaned
// issue survives the failed edge write.
//
// MUTATION-VERIFY: revert create.go to the pre-fix shape (self-committing
// store.CreateIssue + best-effort store.AddDependency with WarnError), and these
// tests FAIL — the issue is committed, the edge is dropped, RunE returns nil, and
// the orphaned issue is found by SearchIssues.

// faultCreateDepStore embeds the real DoltStorage and, for the transactional path,
// wraps the tx so that the Nth AddDependency (failOnAddDep, 1-based across the
// whole run) returns an error — standing in for a mid-sequence edge-write failure
// (serialization conflict 40001, constraint violation, connection blip). All
// other methods delegate to the embedded store. It also faults the
// non-transactional store.AddDependency so a reverted (pre-fix) create.go that
// calls store.AddDependency directly still hits the injected failure — this is
// what makes the mutation-verify honest.
type faultCreateDepStore struct {
	storage.DoltStorage
	addDepCalls  int
	failOnAddDep int
}

var errInjectedCreateDepFailure = errors.New("injected dependency failure (a8d14 test)")

func (f *faultCreateDepStore) RunInTransaction(ctx context.Context, commitMsg string, fn func(tx storage.Transaction) error) error {
	return f.DoltStorage.RunInTransaction(ctx, commitMsg, func(tx storage.Transaction) error {
		return fn(&faultCreateDepTx{Transaction: tx, parent: f})
	})
}

func (f *faultCreateDepStore) AddDependency(ctx context.Context, dep *types.Dependency, actor string) error {
	f.addDepCalls++
	if f.addDepCalls == f.failOnAddDep {
		return errInjectedCreateDepFailure
	}
	return f.DoltStorage.AddDependency(ctx, dep, actor)
}

type faultCreateDepTx struct {
	storage.Transaction
	parent *faultCreateDepStore
}

func (t *faultCreateDepTx) AddDependency(ctx context.Context, dep *types.Dependency, actor string) error {
	t.parent.addDepCalls++
	if t.parent.addDepCalls == t.parent.failOnAddDep {
		return errInjectedCreateDepFailure
	}
	return t.Transaction.AddDependency(ctx, dep, actor)
}

// runCreateWithFault sets up the real store + globals, injects the fault, sets
// the given create flags, runs createCmd.RunE for the title, and returns the
// RunE error. Flags are reset after the run so state does not bleed.
func runCreateWithFault(t *testing.T, real storage.DoltStorage, failOnAddDep int, title string, flags map[string]string) error {
	t.Helper()
	fault := &faultCreateDepStore{DoltStorage: real, failOnAddDep: failOnAddDep}

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
		for name := range flags {
			_ = createCmd.Flags().Set(name, defaultCreateFlag(name))
		}
	})

	store = fault
	rootCtx = context.Background()
	jsonOutput = false
	readonlyMode = false
	actor = "test-actor"
	for name, val := range flags {
		if err := createCmd.Flags().Set(name, val); err != nil {
			t.Fatalf("set --%s=%s: %v", name, val, err)
		}
	}

	var runErr error
	_ = captureStdout(t, func() error {
		runErr = createCmd.RunE(createCmd, []string{title})
		return nil
	})
	return runErr
}

// assertNoOrphanCreated fails if any open issue with the given title survived a
// failed create — the pre-fix bug committed the issue before the edge failed.
func assertNoOrphanCreated(t *testing.T, real storage.DoltStorage, title string) {
	t.Helper()
	ctx := context.Background()
	issues, err := real.SearchIssues(ctx, title, types.IssueFilter{})
	if err != nil {
		t.Fatalf("SearchIssues: %v", err)
	}
	for _, iss := range issues {
		if iss.Title == title {
			t.Errorf("REGRESSION (a8d14): orphaned issue %s (%q) committed despite the edge write failing — a partial create at exit 0 left a durable issue MISSING its declared edge", iss.ID, iss.Title)
		}
	}
}

// TestCreateWithDepIsAtomic_a8d14 forces the --deps edge write to fail and
// asserts the create is all-or-nothing: RunE errors and no orphan issue remains.
func TestCreateWithDepIsAtomic_a8d14(t *testing.T) {
	tmpDir := t.TempDir()
	real := newTestStore(t, filepath.Join(tmpDir, ".beads", "beads.db"))
	ctx := context.Background()

	// A pre-existing target the new issue will declare a dependency on.
	if err := real.CreateIssue(ctx, &types.Issue{
		ID: "test-target", Title: "dep target", Status: types.StatusOpen,
		Priority: 2, IssueType: types.TypeTask,
	}, "test"); err != nil {
		t.Fatalf("create target: %v", err)
	}

	const title = "atomic dep create a8d14"
	// The only AddDependency this run issues is the --deps edge -> fail the 1st.
	err := runCreateWithFault(t, real, 1, title, map[string]string{
		"deps": "blocks:test-target",
	})
	if err == nil {
		t.Fatalf("expected a non-nil RunE error when the --deps edge write fails; got nil (pre-fix bug returns nil and drops the edge)")
	}
	assertNoOrphanCreated(t, real, title)
}

// TestCreateWithParentIsAtomic_a8d14 forces the --parent parent-child edge write
// to fail and asserts the child issue is not left orphaned.
func TestCreateWithParentIsAtomic_a8d14(t *testing.T) {
	tmpDir := t.TempDir()
	real := newTestStore(t, filepath.Join(tmpDir, ".beads", "beads.db"))
	ctx := context.Background()

	// A parent to hang the child under. Use a plain task so the closed-parent
	// guard does not short-circuit before the create.
	if err := real.CreateIssue(ctx, &types.Issue{
		ID: "test-parent", Title: "parent", Status: types.StatusOpen,
		Priority: 2, IssueType: types.TypeTask,
	}, "test"); err != nil {
		t.Fatalf("create parent: %v", err)
	}

	const title = "atomic child create a8d14"
	// The only AddDependency this run issues is the parent-child edge -> fail 1st.
	err := runCreateWithFault(t, real, 1, title, map[string]string{
		"parent": "test-parent",
	})
	if err == nil {
		t.Fatalf("expected a non-nil RunE error when the parent-child edge write fails; got nil (pre-fix bug returns nil and drops the edge)")
	}
	assertNoOrphanCreated(t, real, title)
}
