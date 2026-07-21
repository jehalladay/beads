package main

import (
	"context"
	"fmt"
	"testing"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
)

// beads-ivnqm: `bd gate create` DIRECT path must write the gate issue and its
// blocking dependency ATOMICALLY, matching its proxied twin
// (gate_proxied_server.go, single uw.Commit). Before the fix the two writes ran
// as separate autocommitting store calls (store.CreateIssue then
// store.AddDependency), so an AddDependency failure AFTER CreateIssue committed
// left an ORPHAN gate — the target stayed unblocked (still in bd ready) while
// the user believed it was gated, and a re-run minted a second orphan. This is
// the multi-write-atomicity-twin lens (siblings: beads-ary2n swarm create;
// precedent beads-njnw LinkAndClose).
//
// The fake models the two backends' commit semantics exactly:
//   - store.CreateIssue AUTOCOMMITS (writes straight to the issues map), as the
//     real DoltStore.CreateIssue does (doltAddAndCommit).
//   - RunInTransaction applies writes to a CLONE and only copies back on a nil
//     return, so a tx-scoped failure rolls the gate back.
//
// Mutation proof: revert the fix (go back to the standalone store.CreateIssue +
// store.AddDependency autocommits) and the injected AddDependency fault leaves
// an orphan gate in the store → the "no orphan gate" assertion goes RED. With
// the transact() wrapping, the whole op rolls back → zero gate issues remain.
type gateAtomicFakeStore struct {
	storage.DoltStorage
	issues        map[string]*types.Issue
	deps          []*types.Dependency
	nextID        int
	failAddDep    bool // inject an AddDependency fault (post-CreateIssue)
	directDepCall bool // set if the DIRECT (autocommitting) AddDependency was hit
}

func newGateAtomicFakeStore() *gateAtomicFakeStore {
	return &gateAtomicFakeStore{issues: make(map[string]*types.Issue)}
}

func (s *gateAtomicFakeStore) mint(issue *types.Issue) string {
	s.nextID++
	if issue.IssueType == types.IssueType("gate") {
		return fmt.Sprintf("gt-%d", s.nextID)
	}
	return fmt.Sprintf("iv-%d", s.nextID)
}

func (s *gateAtomicFakeStore) CreateIssue(_ context.Context, issue *types.Issue, _ string) error {
	cp := *issue
	if cp.ID == "" {
		cp.ID = s.mint(&cp)
	}
	issue.ID = cp.ID
	s.issues[cp.ID] = &cp
	return nil
}

func (s *gateAtomicFakeStore) GetIssue(_ context.Context, id string) (*types.Issue, error) {
	issue, ok := s.issues[id]
	if !ok {
		return nil, storage.ErrNotFound
	}
	cp := *issue
	return &cp, nil
}

// AddDependency is the DIRECT autocommit path. A pre-fix (reverted) gate create
// reaches this; the fixed code only ever calls tx.AddDependency.
func (s *gateAtomicFakeStore) AddDependency(_ context.Context, dep *types.Dependency, _ string) error {
	s.directDepCall = true
	if s.failAddDep {
		return fmt.Errorf("injected direct AddDependency fault")
	}
	cp := *dep
	s.deps = append(s.deps, &cp)
	return nil
}

func (s *gateAtomicFakeStore) Commit(_ context.Context, _ string) error { return nil }

func (s *gateAtomicFakeStore) RunInTransaction(ctx context.Context, _ string, fn func(storage.Transaction) error) error {
	clone := &gateAtomicFakeStore{
		issues:     make(map[string]*types.Issue, len(s.issues)),
		deps:       append([]*types.Dependency(nil), s.deps...),
		nextID:     s.nextID,
		failAddDep: s.failAddDep,
	}
	for id, issue := range s.issues {
		cp := *issue
		clone.issues[id] = &cp
	}
	tx := &gateAtomicFakeTx{store: clone}
	if err := fn(tx); err != nil {
		return err // rollback: discard the clone
	}
	// commit: copy the clone's state back
	s.issues = clone.issues
	s.deps = clone.deps
	s.nextID = clone.nextID
	return nil
}

func (s *gateAtomicFakeStore) gateCount() int {
	n := 0
	for _, issue := range s.issues {
		if issue.IssueType == types.IssueType("gate") {
			n++
		}
	}
	return n
}

type gateAtomicFakeTx struct {
	storage.Transaction
	store *gateAtomicFakeStore
}

func (tx *gateAtomicFakeTx) CreateIssue(ctx context.Context, issue *types.Issue, actor string) error {
	return tx.store.CreateIssue(ctx, issue, actor)
}

func (tx *gateAtomicFakeTx) AddDependency(_ context.Context, dep *types.Dependency, _ string) error {
	if tx.store.failAddDep {
		return fmt.Errorf("injected tx AddDependency fault")
	}
	cp := *dep
	tx.store.deps = append(tx.store.deps, &cp)
	return nil
}

func withGateCreateFlags(t *testing.T, blocks string) {
	t.Helper()
	set := func(name, val string) {
		if err := gateCreateCmd.Flags().Set(name, val); err != nil {
			t.Fatalf("set --%s: %v", name, err)
		}
	}
	set("blocks", blocks)
	set("type", "human")
	set("reason", "")
	set("await-id", "")
	set("timeout", "")
	origJSON := jsonOutput
	jsonOutput = false
	t.Cleanup(func() {
		// restore flag defaults + global so sibling tests are unaffected
		_ = gateCreateCmd.Flags().Set("blocks", "")
		_ = gateCreateCmd.Flags().Set("type", "human")
		jsonOutput = origJSON
	})
}

func withGateAtomicStore(t *testing.T) *gateAtomicFakeStore {
	t.Helper()
	fake := newGateAtomicFakeStore()
	oldStore, oldCtx, oldActor := store, rootCtx, actor
	store, rootCtx, actor = fake, context.Background(), "gate-atomic-test"
	t.Cleanup(func() { store, rootCtx, actor = oldStore, oldCtx, oldActor })
	return fake
}

// TestGateCreateDirectAtomicRollsBackOnDepFailure is the beads-ivnqm teeth: when
// the blocking-dependency write fails after the gate issue is staged, the whole
// gate create must roll back — leaving ZERO orphan gate issues. Reverting the
// transact() wrapping (restoring the separate store.CreateIssue +
// store.AddDependency autocommits) makes this RED (the gate persists as an
// orphan).
func TestGateCreateDirectAtomicRollsBackOnDepFailure(t *testing.T) {
	fake := withGateAtomicStore(t)
	target := &types.Issue{Title: "target", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask}
	if err := fake.CreateIssue(context.Background(), target, actor); err != nil {
		t.Fatalf("seed target: %v", err)
	}
	fake.failAddDep = true

	withGateCreateFlags(t, target.ID)

	err := gateCreateCmd.RunE(gateCreateCmd, nil)
	if err == nil {
		t.Fatal("expected gate create to fail when the blocking dependency write fails")
	}
	// The whole operation must roll back: no orphan gate persists.
	if got := fake.gateCount(); got != 0 {
		t.Errorf("orphan gate leaked: gate issue count = %d, want 0 (atomicity broken — the target is left silently unblocked)", got)
	}
	// And no dangling blocking edge.
	if len(fake.deps) != 0 {
		t.Errorf("dependency count = %d, want 0 after rollback", len(fake.deps))
	}
	// The fix must NOT reach the direct autocommit AddDependency path.
	if fake.directDepCall {
		t.Error("direct store.AddDependency was called — the create-sequence is not wrapped in a transaction (beads-ivnqm regression)")
	}
}

// TestGateCreateDirectAtomicCommitsBothOnSuccess pins the happy path: a
// successful gate create persists EXACTLY one gate issue and EXACTLY one
// blocking edge target->gate (guards against both under-commit and the
// re-run-makes-a-second-orphan duplication the non-atomic path enabled).
func TestGateCreateDirectAtomicCommitsBothOnSuccess(t *testing.T) {
	fake := withGateAtomicStore(t)
	target := &types.Issue{Title: "target ok", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask}
	if err := fake.CreateIssue(context.Background(), target, actor); err != nil {
		t.Fatalf("seed target: %v", err)
	}

	withGateCreateFlags(t, target.ID)

	if err := gateCreateCmd.RunE(gateCreateCmd, nil); err != nil {
		t.Fatalf("gate create: %v", err)
	}
	if got := fake.gateCount(); got != 1 {
		t.Fatalf("gate issue count = %d, want 1", got)
	}
	if len(fake.deps) != 1 {
		t.Fatalf("dependency count = %d, want 1", len(fake.deps))
	}
	if fake.deps[0].IssueID != target.ID || fake.deps[0].Type != types.DepBlocks {
		t.Errorf("edge = {issue:%s type:%s}, want {issue:%s type:blocks}", fake.deps[0].IssueID, fake.deps[0].Type, target.ID)
	}
	// The blocking edge must point at the created gate.
	if _, ok := fake.issues[fake.deps[0].DependsOnID]; !ok {
		t.Errorf("blocking edge depends-on %q is not a persisted issue", fake.deps[0].DependsOnID)
	}
}
