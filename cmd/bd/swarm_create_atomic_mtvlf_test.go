//go:build cgo

package main

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
)

// beads-mtvlf: the DIRECT `bd swarm` path previously self-committed the
// auto-wrapper epic (and the swarm molecule) via store.CreateIssue and THEN
// linked it with a separate store.AddDependency. An AddDependency failure
// therefore left a durable orphan epic/molecule MISSING its edge at rc!=0 —
// while the atomic PROXIED twin (swarm_proxied_server.go runSwarmCreateProxied)
// buffers CreateIssue + AddDependency on ONE UOW and commits once. The fix wraps
// each create+link pair in one transactHonoringAutoCommit (the a8d14 DIRECT
// create-atomicity pattern), so a link failure rolls the created node back too
// and RunE exits non-zero — restoring parity with the proxied path.
//
// These teeth drive the REAL swarmCreateCmd.RunE in-process (the a8d14
// save/restore-globals pattern) against a fault-injecting store whose Nth
// AddDependency fails, exercising the wrapper-epic seam: `bd swarm <single-task>`
// auto-wraps the task as an epic, so the ONLY AddDependency it issues is the
// parent-child link — fault it and assert (a) RunE errors and (b) NO orphaned
// wrapper epic survives.
//
// MUTATION-VERIFY: revert swarm.go to the pre-fix shape (self-committing
// store.CreateIssue + separate store.AddDependency), and this test FAILS — the
// epic is committed, the edge is dropped, RunE returns via HandleErrorRespectJSON
// with the orphan already durable, and the orphaned epic is found by SearchIssues.

type faultSwarmDepStore struct {
	storage.DoltStorage
	addDepCalls  int
	failOnAddDep int
}

var errInjectedSwarmDepFailure = errors.New("injected dependency failure (mtvlf test)")

func (f *faultSwarmDepStore) RunInTransaction(ctx context.Context, commitMsg string, fn func(tx storage.Transaction) error) error {
	return f.DoltStorage.RunInTransaction(ctx, commitMsg, func(tx storage.Transaction) error {
		return fn(&faultSwarmDepTx{Transaction: tx, parent: f})
	})
}

func (f *faultSwarmDepStore) AddDependency(ctx context.Context, dep *types.Dependency, actor string) error {
	f.addDepCalls++
	if f.addDepCalls == f.failOnAddDep {
		return errInjectedSwarmDepFailure
	}
	return f.DoltStorage.AddDependency(ctx, dep, actor)
}

type faultSwarmDepTx struct {
	storage.Transaction
	parent *faultSwarmDepStore
}

func (t *faultSwarmDepTx) AddDependency(ctx context.Context, dep *types.Dependency, actor string) error {
	t.parent.addDepCalls++
	if t.parent.addDepCalls == t.parent.failOnAddDep {
		return errInjectedSwarmDepFailure
	}
	return t.Transaction.AddDependency(ctx, dep, actor)
}

// TestSwarmWrapperEpicIsAtomic_mtvlf forces the wrapper-epic parent-child edge
// write to fail and asserts the auto-wrapped epic is not left orphaned.
func TestSwarmWrapperEpicIsAtomic_mtvlf(t *testing.T) {
	tmpDir := t.TempDir()
	real := newTestStore(t, filepath.Join(tmpDir, ".beads", "beads.db"))
	ctx := context.Background()

	// A plain task to swarm: `bd swarm <task>` auto-wraps a non-epic/non-molecule
	// as an epic, so the ONLY AddDependency in the run is the wrapper parent-child
	// link — the perfect single-edge seam to fault.
	if err := real.CreateIssue(ctx, &types.Issue{
		ID: "test-task", Title: "a task to swarm", Status: types.StatusOpen,
		Priority: 2, IssueType: types.TypeTask,
	}, "test"); err != nil {
		t.Fatalf("create task: %v", err)
	}

	fault := &faultSwarmDepStore{DoltStorage: real, failOnAddDep: 1}

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

	store = fault
	rootCtx = context.Background()
	jsonOutput = false
	readonlyMode = false
	actor = "test-actor"

	var runErr error
	_ = captureStdout(t, func() error {
		runErr = swarmCreateCmd.RunE(swarmCreateCmd, []string{"test-task"})
		return nil
	})
	if runErr == nil {
		t.Fatalf("expected a non-nil RunE error when the wrapper-epic edge write fails; got nil (pre-fix bug returns via HandleErrorRespectJSON with the orphan already durable)")
	}

	// No orphaned auto-wrapper epic must survive the failed link.
	issues, err := real.SearchIssues(ctx, "Swarm Epic", types.IssueFilter{})
	if err != nil {
		t.Fatalf("SearchIssues: %v", err)
	}
	for _, iss := range issues {
		if strings.HasPrefix(iss.Title, "Swarm Epic:") {
			t.Errorf("REGRESSION (mtvlf): orphaned wrapper epic %s (%q) committed despite the parent-child link failing — a partial swarm-wrap at exit!=0 left a durable epic MISSING its child edge", iss.ID, iss.Title)
		}
	}
}
