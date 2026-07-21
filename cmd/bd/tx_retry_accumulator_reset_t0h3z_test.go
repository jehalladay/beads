package main

import (
	"context"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
)

// beads-t0h3z: DoltStore.RunInTransaction wraps the closure in withRetry
// (internal/storage/dolt/store_retry.go), which RE-INVOKES the closure on any
// retryable error (serialization conflict 40001, connection blip). Command
// handlers that declare a result accumulator OUTSIDE the closure and
// append-to/increment it INSIDE — with no reset at closure entry — carry the
// prior attempt's entries into the retry, so the REPORTED result is inflated
// (duplicate IDs, doubled counts) even though the DB state is correct (the SQL
// tx rolls back per attempt). These tests use a fake store whose
// RunInTransaction invokes the closure TWICE (one retry) then succeeds, and
// assert each accumulator is fresh per attempt (last-attempt-wins).

// retryingFakeStore is a storage.DoltStorage whose RunInTransaction invokes the
// closure `attempts` times (simulating withRetry re-invocations). The embedded
// nil interface satisfies the rest of DoltStorage — the tested closures only
// touch DeleteIssue/CreateIssue/AddDependency/UpdateIssue/CloseIssue/GetIssue on
// the tx, which the fake tx implements.
type retryingFakeStore struct {
	storage.DoltStorage
	attempts int
	issues   map[string]*types.Issue
}

func (s *retryingFakeStore) GetIssue(_ context.Context, id string) (*types.Issue, error) {
	if iss, ok := s.issues[id]; ok {
		return iss, nil
	}
	return nil, nil
}

func (s *retryingFakeStore) RunInTransaction(ctx context.Context, _ string, fn func(storage.Transaction) error) error {
	n := s.attempts
	if n < 1 {
		n = 1
	}
	var err error
	for i := 0; i < n; i++ {
		tx := &retryingFakeTx{store: s}
		if err = fn(tx); err != nil {
			return err
		}
	}
	return nil
}

// retryingFakeTx accepts every write as a no-op (the DB state is not what this
// test asserts — the accumulator freshness is).
type retryingFakeTx struct {
	storage.Transaction
	store *retryingFakeStore
}

func (tx *retryingFakeTx) DeleteIssue(_ context.Context, _ string) error { return nil }

func (tx *retryingFakeTx) CreateIssue(_ context.Context, _ *types.Issue, _ string) error {
	return nil
}

func (tx *retryingFakeTx) AddDependency(_ context.Context, _ *types.Dependency, _ string) error {
	return nil
}

func (tx *retryingFakeTx) UpdateIssue(_ context.Context, _ string, _ map[string]interface{}, _ string) error {
	return nil
}

func (tx *retryingFakeTx) CloseIssue(_ context.Context, _ string, _ string, _ string, _ string) error {
	return nil
}

func (tx *retryingFakeTx) GetIssue(ctx context.Context, id string) (*types.Issue, error) {
	return tx.store.GetIssue(ctx, id)
}

// TestBurnWispsAccumulatorResetsOnRetry_t0h3z: burnWisps must report exactly the
// input ids once, with no duplicates, after a tx retry. Pre-fix (DeletedIDs
// appended / DeletedCount++ on the outer result) → 6 deleted, [a,b,c,a,b,c].
func TestBurnWispsAccumulatorResetsOnRetry_t0h3z(t *testing.T) {
	s := &retryingFakeStore{attempts: 2}
	ids := []string{"bd-a", "bd-b", "bd-c"}

	result, err := burnWisps(context.Background(), s, ids)
	if err != nil {
		t.Fatalf("burnWisps: %v", err)
	}
	if result.DeletedCount != len(ids) {
		t.Errorf("DeletedCount = %d after one retry, want %d (accumulator not reset per attempt)", result.DeletedCount, len(ids))
	}
	if len(result.DeletedIDs) != len(ids) {
		t.Errorf("DeletedIDs = %v (len %d) after one retry, want %d unique ids", result.DeletedIDs, len(result.DeletedIDs), len(ids))
	}
	seen := make(map[string]int)
	for _, id := range result.DeletedIDs {
		seen[id]++
	}
	for id, n := range seen {
		if n != 1 {
			t.Errorf("DeletedIDs contains %s x%d after retry, want x1 (duplicate ids from accumulating across attempts)", id, n)
		}
	}
}

// TestSquashMoleculeDeletedCountResetsOnRetry_t0h3z: squashMolecule must report
// DeletedCount == len(childIDs) after a retry. Pre-fix (result.DeletedCount++
// inside the closure with no reset) → 2 * len(childIDs).
func TestSquashMoleculeDeletedCountResetsOnRetry_t0h3z(t *testing.T) {
	s := &retryingFakeStore{attempts: 2}
	root := &types.Issue{ID: "bd-root", Title: "root", Priority: 2, Ephemeral: true, Status: types.StatusOpen}
	children := []*types.Issue{
		{ID: "bd-c1", Title: "step 1"},
		{ID: "bd-c2", Title: "step 2"},
	}

	result, err := squashMolecule(context.Background(), s, root, children, false /*keepChildren*/, "", "tester")
	if err != nil {
		t.Fatalf("squashMolecule: %v", err)
	}
	if result.DeletedCount != len(children) {
		t.Errorf("DeletedCount = %d after one retry, want %d (accumulator not reset per attempt)", result.DeletedCount, len(children))
	}
}

// TestBatchResultsResetOnRetry_t0h3z drives the REAL batchCmd.RunE (against a
// double-invoking fake store) and asserts the reported operation count equals
// the input op count. Pre-fix (results declared outside the closure with no
// reset at entry) a withRetry re-invocation appends a second copy of every op,
// so batch reports 2*N operations AND double-flushes the c2pr1 audit trail.
func TestBatchResultsResetOnRetry_t0h3z(t *testing.T) {
	s := &retryingFakeStore{
		attempts: 2,
		issues: map[string]*types.Issue{
			"bd-x": {ID: "bd-x", Title: "x", Status: types.StatusOpen, Priority: 2},
			"bd-y": {ID: "bd-y", Title: "y", Status: types.StatusOpen, Priority: 2},
		},
	}

	// Swap the globals batchCmd.RunE reads, restoring on cleanup.
	oldStore, oldCtx, oldJSON := store, rootCtx, jsonOutput
	store = s
	rootCtx = context.Background()
	jsonOutput = false
	t.Cleanup(func() { store, rootCtx, jsonOutput = oldStore, oldCtx, oldJSON })

	var out strings.Builder
	batchCmd.SetOut(&out)
	batchCmd.SetErr(&out)
	batchCmd.SetIn(strings.NewReader("update bd-x assignee=alice\nupdate bd-y assignee=bob\n"))
	t.Cleanup(func() { batchCmd.SetIn(nil); batchCmd.SetOut(nil); batchCmd.SetErr(nil) })

	if err := batchCmd.RunE(batchCmd, nil); err != nil {
		t.Fatalf("batchCmd.RunE: %v\noutput:\n%s", err, out.String())
	}

	// The plaintext summary is "batch: N operations committed". After one retry
	// with the accumulator reset, N must be 2 (the input op count), not 4.
	got := out.String()
	if !strings.Contains(got, "batch: 2 operations committed") {
		t.Errorf("batch reported wrong op count after one retry — want 'batch: 2 operations committed', got:\n%s", got)
	}
	if strings.Contains(got, "batch: 4 operations committed") {
		t.Errorf("batch double-counted operations on tx retry (accumulator not reset per attempt):\n%s", got)
	}
}
