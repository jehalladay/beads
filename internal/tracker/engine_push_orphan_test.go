package tracker

import (
	"context"
	"errors"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// refPersistFlakeStore wraps pureTestStore and fails the external_ref UpdateIssue
// the first failFirst times, then succeeds — modeling a transient local-DB blip
// after a successful external CreateIssue (beads-2cis). GetIssueByExternalRef
// returns nil so the push path takes the willCreate branch.
type refPersistFlakeStore struct {
	*pureTestStore
	failFirst    int
	updateCalls  int
	lastRefSaved string
}

func (s *refPersistFlakeStore) GetIssueByExternalRef(_ context.Context, _ string) (*types.Issue, error) {
	return nil, nil
}

func (s *refPersistFlakeStore) UpdateIssue(ctx context.Context, id string, updates map[string]interface{}, actor string) error {
	s.updateCalls++
	if s.updateCalls <= s.failFirst {
		return errors.New("simulated transient local-DB error on external_ref persist")
	}
	if ref, ok := updates["external_ref"].(string); ok {
		s.lastRefSaved = ref
	}
	return s.pureTestStore.UpdateIssue(ctx, id, updates, actor)
}

// TestPushRetriesExternalRefPersist_TransientRecovers is the beads-2cis guard:
// when the local external_ref write fails transiently after a successful
// external CreateIssue, doPush retries and succeeds — so the ref IS persisted
// (no orphan → next sync won't duplicate) and no error is reported.
func TestPushRetriesExternalRefPersist_TransientRecovers(t *testing.T) {
	ctx := context.Background()

	issue := &types.Issue{
		ID:        "bd-orph1",
		Title:     "needs push",
		Status:    types.StatusOpen,
		IssueType: types.TypeTask,
		Priority:  2,
	}
	store := &refPersistFlakeStore{pureTestStore: newPureTestStore(issue), failFirst: 2}
	tracker := newMockTracker("test")
	engine := NewEngine(tracker, store, "test-actor")

	result, err := engine.Sync(ctx, SyncOptions{Push: true})
	if err != nil {
		t.Fatalf("Sync() error: %v", err)
	}

	if store.updateCalls < 3 {
		t.Errorf("expected the external_ref persist to be retried (>=3 calls: 2 fail + 1 success), got %d", store.updateCalls)
	}
	if store.lastRefSaved == "" {
		t.Errorf("external_ref was never persisted — the retry did not recover the transient failure (orphan → next-sync duplicate)")
	}
	if result.Stats.Errors != 0 {
		t.Errorf("Stats.Errors = %d, want 0 (the retry recovered the transient persist failure)", result.Stats.Errors)
	}
	if result.Stats.Created != 1 {
		t.Errorf("Stats.Created = %d, want 1", result.Stats.Created)
	}
}

// TestPushExternalRefPersistPersistentFailureSurfaces verifies the other arm:
// when the local persist fails on EVERY retry, the external issue is genuinely
// orphaned — the failure is surfaced as an Error (not silently swallowed), while
// Created is still counted (the external issue does exist).
func TestPushExternalRefPersistPersistentFailureSurfaces(t *testing.T) {
	ctx := context.Background()

	issue := &types.Issue{
		ID:        "bd-orph2",
		Title:     "needs push",
		Status:    types.StatusOpen,
		IssueType: types.TypeTask,
		Priority:  2,
	}
	// failFirst huge → every attempt fails.
	store := &refPersistFlakeStore{pureTestStore: newPureTestStore(issue), failFirst: 1000}
	tracker := newMockTracker("test")

	var warnings []string
	engine := NewEngine(tracker, store, "test-actor")
	engine.OnWarning = func(msg string) { warnings = append(warnings, msg) }

	result, err := engine.Sync(ctx, SyncOptions{Push: true})
	if err != nil {
		t.Fatalf("Sync() error: %v", err)
	}

	if store.updateCalls != externalRefPersistRetries {
		t.Errorf("expected %d persist attempts (bounded retry), got %d", externalRefPersistRetries, store.updateCalls)
	}
	if result.Stats.Errors != 1 {
		t.Errorf("Stats.Errors = %d, want 1 (persistent persist failure must surface, not be swallowed)", result.Stats.Errors)
	}
	if result.Stats.Created != 1 {
		t.Errorf("Stats.Created = %d, want 1 (the external issue was created)", result.Stats.Created)
	}
	sawOrphanWarn := false
	for _, w := range warnings {
		if len(w) > 0 && (contains(w, "orphan") || contains(w, "external_ref")) {
			sawOrphanWarn = true
			break
		}
	}
	if !sawOrphanWarn {
		t.Errorf("expected a warning about the orphaned/unpersisted external_ref, got: %v", warnings)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
