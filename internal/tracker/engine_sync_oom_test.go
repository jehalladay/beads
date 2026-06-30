// Regression guard for beads-r06.13 / OOM-1 (RCA hq-uo7a1).
//
// The four Sync-reachable call sites in engine.go (DetectConflicts, doPull,
// doPush, dependencyIssueResolver) used to call SearchIssues(ctx, "",
// IssueFilter{}) with an empty filter. With filter.Limit==0 that triggers
// search.go's Pattern A: a full 47-column, unbounded SELECT that materializes
// EVERY issue row (plus label hydration) into a slice — four times per
// bidirectional Sync. On a large rig synced in a loop this walked memory to
// 134GB RSS and OOM-crashed the control plane.
//
// These tests wrap the store with a decorator that records any UNBOUNDED
// SearchIssues call (Limit==0) reachable from the sync paths. After the fix
// the sync paths must stream via IterIssues/IterWisps instead, so the
// unbounded-search count must be zero.
//
// Pure-Go (no cgo tag) so the guard runs hermetically in CI and on the eladba
// build pipeline without a Dolt container.
package tracker

import (
	"context"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
)

// oomCountingStore wraps pureTestStore and records every UNBOUNDED
// SearchIssues call (filter.Limit == 0) — the Pattern A full-table
// materialization that caused the 134GB OOM. The streaming iterator methods
// (IterIssues / IterWisps) are served from the same in-memory slice and
// counted separately so the test can assert the sync paths switched to
// streaming.
type oomCountingStore struct {
	*pureTestStore

	unboundedSearches   int
	unboundedSearchSeen []string // query strings, for diagnostics
	iterIssues          int
	iterWisps           int
}

func newOOMCountingStore(issues ...*types.Issue) *oomCountingStore {
	return &oomCountingStore{pureTestStore: newPureTestStore(issues...)}
}

func (s *oomCountingStore) SearchIssues(ctx context.Context, query string, filter types.IssueFilter) ([]*types.Issue, error) {
	if filter.Limit == 0 {
		s.unboundedSearches++
		s.unboundedSearchSeen = append(s.unboundedSearchSeen, query)
	}
	return s.pureTestStore.SearchIssues(ctx, query, filter)
}

// IterIssues streams the in-memory issues (issues table only, matching the
// real DoltStore which queries issues and leaves wisps to IterWisps).
func (s *oomCountingStore) IterIssues(_ context.Context, _ string, filter types.IssueFilter) (storage.Iter[types.Issue], error) {
	s.iterIssues++
	items := append([]*types.Issue(nil), s.pureTestStore.issues...)
	if filter.Limit > 0 && len(items) > filter.Limit {
		items = items[:filter.Limit]
	}
	return storage.NewSliceIter(items), nil
}

// IterWisps streams ephemeral/NoHistory issues. The in-memory store keeps no
// separate wisps table, so this yields nothing — sufficient for the routing
// assertion (the CGO soak/parity tests cover real wisp-merge equivalence).
func (s *oomCountingStore) IterWisps(_ context.Context, _ types.WispFilter) (storage.Iter[types.Issue], error) {
	s.iterWisps++
	return storage.NewSliceIter[types.Issue](nil), nil
}

// GetIssueByExternalRef and GetConfig are needed by doPull. pureTestStore
// embeds a nil storage.Storage, so we provide the minimal behavior here.
func (s *oomCountingStore) GetIssueByExternalRef(_ context.Context, ref string) (*types.Issue, error) {
	for _, iss := range s.pureTestStore.issues {
		if iss.ExternalRef != nil && *iss.ExternalRef == ref {
			return iss, nil
		}
	}
	return nil, nil
}

func (s *oomCountingStore) GetConfig(_ context.Context, _ string) (string, error) {
	return "bd", nil
}

// seedSyncFixture builds a store + tracker with one local issue carrying an
// external ref and an empty external tracker, with last_sync in the past so a
// bidirectional Sync exercises DetectConflicts, doPull and doPush. The empty
// tracker means doPull's import loop is a no-op (no RunInTransaction needed),
// keeping the fixture pure-Go.
func seedSyncFixture(t *testing.T) (*oomCountingStore, *mockTracker, *Engine) {
	t.Helper()
	ref := "https://test.test/EXT-LOCAL1"
	local := &types.Issue{
		ID:          "bd-local1",
		Title:       "Local issue with external ref",
		Status:      types.StatusOpen,
		IssueType:   types.TypeTask,
		Priority:    2,
		ExternalRef: &ref,
		UpdatedAt:   time.Now(),
	}
	store := newOOMCountingStore(local)

	tracker := newMockTracker("test")
	if err := store.SetLocalMetadata(context.Background(),
		tracker.ConfigPrefix()+".last_sync",
		time.Now().Add(-1*time.Hour).Format(time.RFC3339)); err != nil {
		t.Fatalf("SetLocalMetadata error: %v", err)
	}

	return store, tracker, NewEngine(tracker, store, "test-actor")
}

// TestSyncDoesNotUnboundedSearch is the OOM-1 regression guard for the
// DetectConflicts, doPull and doPush sync paths. A full bidirectional Sync
// must not trigger a single unbounded (Limit==0) SearchIssues.
func TestSyncDoesNotUnboundedSearch(t *testing.T) {
	ctx := context.Background()
	store, _, engine := seedSyncFixture(t)

	if _, err := engine.Sync(ctx, SyncOptions{Pull: true, Push: true}); err != nil {
		t.Fatalf("Sync() error: %v", err)
	}

	t.Logf("after Sync: unboundedSearches=%d iterIssues=%d iterWisps=%d",
		store.unboundedSearches, store.iterIssues, store.iterWisps)
	if store.unboundedSearches != 0 {
		t.Fatalf("Sync triggered %d unbounded (Limit==0) SearchIssues calls (queries=%v); "+
			"sync paths must stream via IterIssues/IterWisps to bound memory (beads-r06.13 / OOM-1)",
			store.unboundedSearches, store.unboundedSearchSeen)
	}
}

// TestDependencyIssueResolverDoesNotUnboundedSearch covers the fourth
// Sync-reachable call site (engine.go dependencyIssueResolver), which doPull
// invokes via createDependencies when pulled issues carry dependencies.
func TestDependencyIssueResolverDoesNotUnboundedSearch(t *testing.T) {
	ctx := context.Background()
	store, _, engine := seedSyncFixture(t)

	if _, err := engine.dependencyIssueResolver(ctx, nil); err != nil {
		t.Fatalf("dependencyIssueResolver error: %v", err)
	}

	t.Logf("after dependencyIssueResolver: unboundedSearches=%d iterIssues=%d iterWisps=%d",
		store.unboundedSearches, store.iterIssues, store.iterWisps)
	if store.unboundedSearches != 0 {
		t.Fatalf("dependencyIssueResolver triggered %d unbounded (Limit==0) SearchIssues calls "+
			"(queries=%v); must stream to bound memory (beads-r06.13 / OOM-1)",
			store.unboundedSearches, store.unboundedSearchSeen)
	}
}
