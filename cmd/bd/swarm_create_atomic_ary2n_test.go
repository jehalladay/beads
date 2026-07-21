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

// beads-ary2n: the DIRECT `bd swarm create` path previously performed its 2–4
// write sequence as SEPARATE autocommitting store.CreateIssue / store.AddDependency
// calls with NO enclosing transaction, while the atomic PROXIED twin buffers the
// same writes on one UOW and commits once. A mid-sequence failure on a LINK write
// therefore committed a partial swarm:
//   - molecule branch: CreateIssue(swarmMol) commits, then AddDependency(relates-to)
//     fails -> orphaned molecule with NO relates-to edge to the epic. findExistingSwarm
//     matches ONLY on that link, so the orphan is invisible -> a retry creates a
//     SECOND molecule; repeat failures accumulate undiscoverable duplicate orphans.
//   - auto-wrap branch: CreateIssue(wrapperEpic) commits, then AddDependency(parent-child)
//     fails -> orphaned "Swarm Epic:" wrapper, original issue never linked as its child.
//
// The fix wraps each create+link pair in store.RunInTransaction (mirroring the
// proxied twin + graph_apply.go precedent) so the pair is all-or-nothing.
//
// These teeth drive the REAL swarmCreateCmd.RunE in-process (the gate_test.go
// save/restore-globals pattern) against a fault-injecting store whose second
// AddDependency inside any transaction fails, and assert NO orphan is left.
//
// MUTATION-VERIFY: revert the swarm.go create+link pairs to two separate
// autocommitting store.* calls (drop the RunInTransaction wrappers) and these
// tests FAIL — the orphaned molecule / wrapper epic persists after the link fails.

// faultLinkStore embeds the real DoltStorage and, for the transactional path,
// wraps the tx so that the Nth AddDependency (failOnAddDep) returns an error —
// standing in for a mid-sequence link failure (serialization conflict 40001,
// constraint, connection blip). All other methods delegate to the embedded store.
type faultLinkStore struct {
	storage.DoltStorage
	addDepCalls  int
	failOnAddDep int // fail the Nth AddDependency across the whole run (1-based)
}

var errInjectedLinkFailure = errors.New("injected link failure (ary2n test)")

func (f *faultLinkStore) RunInTransaction(ctx context.Context, commitMsg string, fn func(tx storage.Transaction) error) error {
	return f.DoltStorage.RunInTransaction(ctx, commitMsg, func(tx storage.Transaction) error {
		return fn(&faultLinkTx{Transaction: tx, parent: f})
	})
}

// AddDependency also faults on the non-transactional (pre-fix) path, so a
// reverted swarm.go that calls store.AddDependency directly still hits the
// injected failure — this is what makes the mutation-verify honest.
func (f *faultLinkStore) AddDependency(ctx context.Context, dep *types.Dependency, actor string) error {
	f.addDepCalls++
	if f.addDepCalls == f.failOnAddDep {
		return errInjectedLinkFailure
	}
	return f.DoltStorage.AddDependency(ctx, dep, actor)
}

type faultLinkTx struct {
	storage.Transaction
	parent *faultLinkStore
}

func (t *faultLinkTx) AddDependency(ctx context.Context, dep *types.Dependency, actor string) error {
	t.parent.addDepCalls++
	if t.parent.addDepCalls == t.parent.failOnAddDep {
		return errInjectedLinkFailure
	}
	return t.Transaction.AddDependency(ctx, dep, actor)
}

// runSwarmCreateWithFault sets up the real store + globals, injects the fault,
// runs swarmCreateCmd.RunE for the given arg, and returns the RunE error.
func runSwarmCreateWithFault(t *testing.T, real storage.DoltStorage, failOnAddDep int, arg string) error {
	t.Helper()
	fault := &faultLinkStore{DoltStorage: real, failOnAddDep: failOnAddDep}

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
	if err := swarmCreateCmd.Flags().Set("coordinator", "observer/"); err != nil {
		t.Fatalf("set coordinator: %v", err)
	}
	if err := swarmCreateCmd.Flags().Set("force", "false"); err != nil {
		t.Fatalf("set force: %v", err)
	}

	// Silence stdout — RunE prints a summary/warnings we don't assert on here.
	var runErr error
	_ = captureStdout(t, func() error {
		runErr = swarmCreateCmd.RunE(swarmCreateCmd, []string{arg})
		return nil
	})
	return runErr
}

// TestSwarmMoleculeCreateIsAtomic_ary2n: an epic with a swarmable child; force
// the swarm-molecule's relates-to link (the write AFTER the molecule create) to
// fail, then assert no orphaned molecule survives.
func TestSwarmMoleculeCreateIsAtomic_ary2n(t *testing.T) {
	tmpDir := t.TempDir()
	real := newTestStore(t, filepath.Join(tmpDir, ".beads", "beads.db"))
	ctx := context.Background()

	// Epic + one child so analyzeEpicForSwarm returns Swarmable (a childless epic
	// is rejected before the molecule create).
	if err := real.CreateIssue(ctx, &types.Issue{
		ID: "sw-epic", Title: "sw-epic", Status: types.StatusOpen,
		Priority: 1, IssueType: types.TypeEpic,
	}, "test"); err != nil {
		t.Fatalf("create epic: %v", err)
	}
	if err := real.CreateIssue(ctx, &types.Issue{
		ID: "sw-child", Title: "sw-child", Status: types.StatusOpen,
		Priority: 1, IssueType: types.TypeTask,
	}, "test"); err != nil {
		t.Fatalf("create child: %v", err)
	}
	if err := real.AddDependency(ctx, &types.Dependency{
		IssueID: "sw-child", DependsOnID: "sw-epic", Type: types.DepParentChild,
	}, "test"); err != nil {
		t.Fatalf("link child->epic: %v", err)
	}

	// The epic already exists, so RunE takes the NON-auto-wrap path: the only
	// AddDependency it issues is the swarm molecule's relates-to link -> fail the
	// 1st AddDependency of the run.
	err := runSwarmCreateWithFault(t, real, 1, "sw-epic")
	if err == nil {
		t.Fatal("expected swarm create to fail on the injected relates-to link failure")
	}

	// ATOMICITY: no swarm molecule may have been committed. The orphan the bug
	// produces has NO relates-to edge to the epic (that link is exactly what
	// failed) — so it is INVISIBLE to findExistingSwarm AND to
	// GetDependents(epic). That invisibility IS the bug (retry can't find it →
	// duplicates), so we must scan ALL issues for a stray molecule, not the
	// epic's dependents. A survivor means CreateIssue(swarmMol) was not rolled
	// back.
	all, lerr := real.SearchIssues(ctx, "", types.IssueFilter{})
	if lerr != nil {
		t.Fatalf("SearchIssues: %v", lerr)
	}
	for _, iss := range all {
		if iss.IssueType == "molecule" {
			t.Errorf("REGRESSION (ary2n): orphaned swarm molecule %s (%q) committed despite the relates-to link failing — invisible to findExistingSwarm (no edge to the epic), so a retry creates a duplicate", iss.ID, iss.Title)
		}
	}
	// Sanity: findExistingSwarm (the retry discovery path) must also see no swarm.
	sw, ferr := findExistingSwarm(ctx, real, "sw-epic")
	if ferr != nil {
		t.Fatalf("findExistingSwarm: %v", ferr)
	}
	if sw != nil {
		t.Errorf("REGRESSION (ary2n): findExistingSwarm found molecule %s linked to the epic despite the link write failing", sw.ID)
	}
}

// TestSwarmWrapperEpicCreateIsAtomic_ary2n: a single task (not an epic) triggers
// the auto-wrap branch. Force the parent-child link (the write AFTER the wrapper
// epic create) to fail, then assert no orphaned wrapper epic survives.
func TestSwarmWrapperEpicCreateIsAtomic_ary2n(t *testing.T) {
	tmpDir := t.TempDir()
	real := newTestStore(t, filepath.Join(tmpDir, ".beads", "beads.db"))
	ctx := context.Background()

	if err := real.CreateIssue(ctx, &types.Issue{
		ID: "sw-task", Title: "single task", Status: types.StatusOpen,
		Priority: 1, IssueType: types.TypeTask,
	}, "test"); err != nil {
		t.Fatalf("create task: %v", err)
	}

	// A single non-epic input -> auto-wrap branch: the FIRST AddDependency of the
	// run is the parent-child link right after the wrapper epic create.
	err := runSwarmCreateWithFault(t, real, 1, "sw-task")
	if err == nil {
		t.Fatal("expected swarm create to fail on the injected parent-child link failure")
	}

	// ATOMICITY: no wrapper epic may have been committed. Scan for any epic titled
	// "Swarm Epic:" — a survivor is the orphaned wrapper beads-ary2n reports.
	all, lerr := real.SearchIssues(ctx, "", types.IssueFilter{})
	if lerr != nil {
		t.Fatalf("SearchIssues: %v", lerr)
	}
	for _, iss := range all {
		if iss.IssueType == types.TypeEpic && iss.ID != "sw-task" {
			t.Errorf("REGRESSION (ary2n): wrapper epic %s (%q) committed despite the parent-child link failing — create was not rolled back", iss.ID, iss.Title)
		}
	}
}
