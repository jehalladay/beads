package storage

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/steveyegge/beads/internal/hooks"
	"github.com/steveyegge/beads/internal/types"
)

// mutatingHookStore is a fake DoltStorage that exercises the store-level
// mutation methods of HookFiringStore. Each mutation returns mutationErr
// (if set) before touching state, so tests can drive the error-passthrough
// path (no hook fires). GetIssue serves from the issues map; a missing id
// yields a not-found error, which drives the best-effort refetch-error
// branch of fireHookByID / fireDependencyHookByID.
type mutatingHookStore struct {
	DoltStorage
	issues      map[string]*types.Issue
	deps        map[string][]*types.Dependency
	mutationErr error
}

func (s *mutatingHookStore) UpdateIssue(_ context.Context, _ string, _ map[string]interface{}, _ string) error {
	return s.mutationErr
}

func (s *mutatingHookStore) ReopenIssue(_ context.Context, _ string, _ string, _ string) error {
	return s.mutationErr
}

func (s *mutatingHookStore) UpdateIssueType(_ context.Context, _ string, _ string, _ string) error {
	return s.mutationErr
}

func (s *mutatingHookStore) CloseIssue(_ context.Context, _ string, _ string, _ string, _ string) error {
	return s.mutationErr
}

func (s *mutatingHookStore) AddDependency(_ context.Context, _ *types.Dependency, _ string) error {
	return s.mutationErr
}

func (s *mutatingHookStore) RemoveDependency(_ context.Context, _ string, _ string, _ string) error {
	return s.mutationErr
}

func (s *mutatingHookStore) AddLabel(_ context.Context, _ string, _ string, _ string) error {
	return s.mutationErr
}

func (s *mutatingHookStore) RemoveLabel(_ context.Context, _ string, _ string, _ string) error {
	return s.mutationErr
}

func (s *mutatingHookStore) AddIssueComment(_ context.Context, _ string, author, text string) (*types.Comment, error) {
	if s.mutationErr != nil {
		return nil, s.mutationErr
	}
	return &types.Comment{Author: author, Text: text}, nil
}

func (s *mutatingHookStore) GetIssue(_ context.Context, id string) (*types.Issue, error) {
	issue, ok := s.issues[id]
	if !ok {
		return nil, errors.New("not found")
	}
	return cloneIssueForHook(issue), nil
}

func (s *mutatingHookStore) GetDependencyRecords(_ context.Context, id string) ([]*types.Dependency, error) {
	return cloneDependenciesForHook(s.deps[id]), nil
}

func newMutatingStore() *mutatingHookStore {
	return &mutatingHookStore{
		issues: map[string]*types.Issue{
			"issue-1": {ID: "issue-1", IssueType: types.TypeTask},
		},
		deps: map[string][]*types.Dependency{
			"issue-1": {{IssueID: "issue-1", DependsOnID: "issue-2", Type: types.DepBlocks}},
		},
	}
}

func newMutatingHookFiringStore(runner hookRunner) (*HookFiringStore, *mutatingHookStore) {
	inner := newMutatingStore()
	return &HookFiringStore{DoltStorage: inner, inner: inner, runner: runner}, inner
}

// storeMutation drives one store-level mutation and reports the error it
// returned. wantEvent is the hook event expected on the success path.
type storeMutation struct {
	name      string
	wantEvent string
	call      func(store *HookFiringStore) error
}

func storeMutations() []storeMutation {
	ctx := context.Background()
	return []storeMutation{
		{"UpdateIssue", hooks.EventUpdate, func(s *HookFiringStore) error {
			return s.UpdateIssue(ctx, "issue-1", map[string]interface{}{"status": "open"}, "tester")
		}},
		{"ReopenIssue", hooks.EventUpdate, func(s *HookFiringStore) error {
			return s.ReopenIssue(ctx, "issue-1", "reason", "tester")
		}},
		{"UpdateIssueType", hooks.EventUpdate, func(s *HookFiringStore) error {
			return s.UpdateIssueType(ctx, "issue-1", "bug", "tester")
		}},
		{"CloseIssue", hooks.EventClose, func(s *HookFiringStore) error {
			return s.CloseIssue(ctx, "issue-1", "done", "tester", "sess")
		}},
		{"AddDependency", hooks.EventUpdate, func(s *HookFiringStore) error {
			return s.AddDependency(ctx, &types.Dependency{IssueID: "issue-1", DependsOnID: "issue-2", Type: types.DepBlocks}, "tester")
		}},
		{"RemoveDependency", hooks.EventUpdate, func(s *HookFiringStore) error {
			return s.RemoveDependency(ctx, "issue-1", "issue-2", "tester")
		}},
		{"AddLabel", hooks.EventUpdate, func(s *HookFiringStore) error {
			return s.AddLabel(ctx, "issue-1", "urgent", "tester")
		}},
		{"RemoveLabel", hooks.EventUpdate, func(s *HookFiringStore) error {
			return s.RemoveLabel(ctx, "issue-1", "urgent", "tester")
		}},
		{"AddIssueComment", hooks.EventUpdate, func(s *HookFiringStore) error {
			_, err := s.AddIssueComment(ctx, "issue-1", "tester", "hi")
			return err
		}},
	}
}

func TestHookFiringStoreMutationsFireHookOnSuccess(t *testing.T) {
	for _, m := range storeMutations() {
		t.Run(m.name, func(t *testing.T) {
			runner := &recordingHookRunner{}
			store, _ := newMutatingHookFiringStore(runner)

			if err := m.call(store); err != nil {
				t.Fatalf("%s returned error: %v", m.name, err)
			}
			if len(runner.events) != 1 {
				t.Fatalf("%s fired %d hooks, want 1 (%v)", m.name, len(runner.events), runner.events)
			}
			if runner.events[0] != m.wantEvent {
				t.Fatalf("%s fired %q, want %q", m.name, runner.events[0], m.wantEvent)
			}
			if runner.issues[0] == nil || runner.issues[0].ID != "issue-1" {
				t.Fatalf("%s fired hook with wrong issue: %+v", m.name, runner.issues[0])
			}
		})
	}
}

func TestHookFiringStoreMutationsPassThroughErrorWithoutFiring(t *testing.T) {
	wantErr := errors.New("inner mutation failed")
	for _, m := range storeMutations() {
		t.Run(m.name, func(t *testing.T) {
			runner := &recordingHookRunner{}
			inner := newMutatingStore()
			inner.mutationErr = wantErr
			store := &HookFiringStore{DoltStorage: inner, inner: inner, runner: runner}

			if err := m.call(store); !errors.Is(err, wantErr) {
				t.Fatalf("%s returned %v, want %v", m.name, err, wantErr)
			}
			if len(runner.events) != 0 {
				t.Fatalf("%s fired %d hooks on failure, want 0", m.name, len(runner.events))
			}
		})
	}
}

func TestHookFiringStoreMutationsNilRunnerNoFire(t *testing.T) {
	// With a nil runner, fireHook* return early — mutations still succeed.
	for _, m := range storeMutations() {
		t.Run(m.name, func(t *testing.T) {
			store, _ := newMutatingHookFiringStore(nil)
			if err := m.call(store); err != nil {
				t.Fatalf("%s with nil runner returned error: %v", m.name, err)
			}
		})
	}
}

func TestHookFiringStoreMutationsRefetchErrorSkipsHook(t *testing.T) {
	// Mutation succeeds but the issue is absent from the store, so the
	// best-effort re-fetch in fireHookByID / fireDependencyHookByID fails
	// and the hook is skipped without surfacing an error.
	for _, m := range storeMutations() {
		t.Run(m.name, func(t *testing.T) {
			runner := &recordingHookRunner{}
			inner := &mutatingHookStore{
				issues: map[string]*types.Issue{}, // empty → GetIssue not-found
				deps:   map[string][]*types.Dependency{},
			}
			store := &HookFiringStore{DoltStorage: inner, inner: inner, runner: runner}

			if err := m.call(store); err != nil {
				t.Fatalf("%s returned error: %v", m.name, err)
			}
			if len(runner.events) != 0 {
				t.Fatalf("%s fired %d hooks despite refetch error, want 0", m.name, len(runner.events))
			}
		})
	}
}

// mutatingHookTx is a fake Transaction that records each mutation as a
// success (returning txErr when set), and serves GetIssue from the store's
// issues map. HookFiringStore.RunInTransaction wraps it in a
// hookTrackingTransaction, so exercising its mutations accumulates pending
// hooks that fire after commit.
type mutatingHookTx struct {
	Transaction
	issues map[string]*types.Issue
	deps   map[string][]*types.Dependency
	txErr  error
}

func (tx *mutatingHookTx) UpdateIssue(_ context.Context, _ string, _ map[string]interface{}, _ string) error {
	return tx.txErr
}

func (tx *mutatingHookTx) CloseIssue(_ context.Context, _ string, _ string, _ string, _ string) error {
	return tx.txErr
}

func (tx *mutatingHookTx) AddDependencyWithOptions(_ context.Context, _ *types.Dependency, _ string, _ DependencyAddOptions) error {
	return tx.txErr
}

func (tx *mutatingHookTx) RemoveDependency(_ context.Context, _ string, _ string, _ string) error {
	return tx.txErr
}

func (tx *mutatingHookTx) AddLabel(_ context.Context, _ string, _ string, _ string) error {
	return tx.txErr
}

func (tx *mutatingHookTx) RemoveLabel(_ context.Context, _ string, _ string, _ string) error {
	return tx.txErr
}

func (tx *mutatingHookTx) AddComment(_ context.Context, _ string, _ string, _ string) error {
	return tx.txErr
}

func (tx *mutatingHookTx) DeleteIssue(_ context.Context, _ string) error {
	return tx.txErr
}

func (tx *mutatingHookTx) GetIssue(_ context.Context, id string) (*types.Issue, error) {
	issue, ok := tx.issues[id]
	if !ok {
		return nil, errors.New("not found")
	}
	return cloneIssueForHook(issue), nil
}

func (tx *mutatingHookTx) GetDependencyRecords(_ context.Context, id string) ([]*types.Dependency, error) {
	return cloneDependenciesForHook(tx.deps[id]), nil
}

// txHookStore is a fake DoltStorage whose RunInTransaction hands the callback
// a mutatingHookTx, letting tests drive hookTrackingTransaction's methods.
type txHookStore struct {
	DoltStorage
	tx *mutatingHookTx
}

func (s *txHookStore) RunInTransaction(ctx context.Context, _ string, fn func(tx Transaction) error) error {
	return fn(s.tx)
}

func newTxHookFiringStore(runner hookRunner, txErr error) *HookFiringStore {
	fake := &mutatingHookTx{
		issues: map[string]*types.Issue{
			"issue-1": {ID: "issue-1", IssueType: types.TypeTask},
		},
		deps: map[string][]*types.Dependency{
			"issue-1": {{IssueID: "issue-1", DependsOnID: "issue-2", Type: types.DepBlocks}},
		},
		txErr: txErr,
	}
	inner := &txHookStore{tx: fake}
	return &HookFiringStore{DoltStorage: inner, inner: inner, runner: runner}
}

type txMutation struct {
	name      string
	wantEvent string
	call      func(ctx context.Context, tx Transaction) error
}

func txMutations() []txMutation {
	return []txMutation{
		{"UpdateIssue", hooks.EventUpdate, func(ctx context.Context, tx Transaction) error {
			return tx.UpdateIssue(ctx, "issue-1", map[string]interface{}{"status": "open"}, "tester")
		}},
		{"CloseIssue", hooks.EventClose, func(ctx context.Context, tx Transaction) error {
			return tx.CloseIssue(ctx, "issue-1", "done", "tester", "sess")
		}},
		{"AddDependency", hooks.EventUpdate, func(ctx context.Context, tx Transaction) error {
			return tx.AddDependency(ctx, &types.Dependency{IssueID: "issue-1", DependsOnID: "issue-2", Type: types.DepBlocks}, "tester")
		}},
		{"AddDependencyWithOptions", hooks.EventUpdate, func(ctx context.Context, tx Transaction) error {
			return tx.AddDependencyWithOptions(ctx, &types.Dependency{IssueID: "issue-1", DependsOnID: "issue-2", Type: types.DepBlocks}, "tester", DependencyAddOptions{})
		}},
		{"RemoveDependency", hooks.EventUpdate, func(ctx context.Context, tx Transaction) error {
			return tx.RemoveDependency(ctx, "issue-1", "issue-2", "tester")
		}},
		{"AddLabel", hooks.EventUpdate, func(ctx context.Context, tx Transaction) error {
			return tx.AddLabel(ctx, "issue-1", "urgent", "tester")
		}},
		{"RemoveLabel", hooks.EventUpdate, func(ctx context.Context, tx Transaction) error {
			return tx.RemoveLabel(ctx, "issue-1", "urgent", "tester")
		}},
		{"AddComment", hooks.EventUpdate, func(ctx context.Context, tx Transaction) error {
			return tx.AddComment(ctx, "issue-1", "tester", "hi")
		}},
	}
}

func TestHookTrackingTransactionMutationsFireAfterCommit(t *testing.T) {
	for _, m := range txMutations() {
		t.Run(m.name, func(t *testing.T) {
			runner := &recordingHookRunner{}
			store := newTxHookFiringStore(runner, nil)

			err := store.RunInTransaction(context.Background(), "test", func(tx Transaction) error {
				return m.call(context.Background(), tx)
			})
			if err != nil {
				t.Fatalf("%s: RunInTransaction: %v", m.name, err)
			}
			if len(runner.events) != 1 {
				t.Fatalf("%s fired %d hooks, want 1 (%v)", m.name, len(runner.events), runner.events)
			}
			if runner.events[0] != m.wantEvent {
				t.Fatalf("%s fired %q, want %q", m.name, runner.events[0], m.wantEvent)
			}
		})
	}
}

func TestHookTrackingTransactionMutationsPassThroughError(t *testing.T) {
	wantErr := errors.New("tx mutation failed")
	for _, m := range txMutations() {
		t.Run(m.name, func(t *testing.T) {
			runner := &recordingHookRunner{}
			store := newTxHookFiringStore(runner, wantErr)

			err := store.RunInTransaction(context.Background(), "test", func(tx Transaction) error {
				return m.call(context.Background(), tx)
			})
			if !errors.Is(err, wantErr) {
				t.Fatalf("%s: RunInTransaction err = %v, want %v", m.name, err, wantErr)
			}
			if len(runner.events) != 0 {
				t.Fatalf("%s fired %d hooks after error, want 0", m.name, len(runner.events))
			}
		})
	}
}

func TestHookTrackingTransactionDeleteIssueNoHook(t *testing.T) {
	runner := &recordingHookRunner{}
	store := newTxHookFiringStore(runner, nil)

	err := store.RunInTransaction(context.Background(), "test", func(tx Transaction) error {
		return tx.DeleteIssue(context.Background(), "issue-1")
	})
	if err != nil {
		t.Fatalf("RunInTransaction: %v", err)
	}
	if len(runner.events) != 0 {
		t.Fatalf("DeleteIssue fired %d hooks, want 0 (delete is destructive)", len(runner.events))
	}
}

func TestHookFiringStoreInnerReturnsUnderlyingStore(t *testing.T) {
	inner := newMutatingStore()
	store := &HookFiringStore{DoltStorage: inner, inner: inner, runner: &recordingHookRunner{}}
	if store.Inner() != inner {
		t.Fatalf("Inner() = %v, want the underlying store", store.Inner())
	}
}

func TestUnwrapStoreReturnsInnerForDecorator(t *testing.T) {
	inner := newMutatingStore()
	store := &HookFiringStore{DoltStorage: inner, inner: inner}
	if got := UnwrapStore(store); got != inner {
		t.Fatalf("UnwrapStore(decorator) = %v, want inner store", got)
	}
}

func TestUnwrapStoreReturnsArgumentForNonDecorator(t *testing.T) {
	inner := newMutatingStore()
	if got := UnwrapStore(inner); !reflect.DeepEqual(got, DoltStorage(inner)) {
		t.Fatalf("UnwrapStore(non-decorator) = %v, want the argument unchanged", got)
	}
}
