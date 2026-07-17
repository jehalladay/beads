// Regression test for beads-y26i: doPull must not silently swallow a local
// write failure. A CreateIssue/UpdateIssue error during pull was only warned
// and `continue`d, never counted in stats.Errors, so a failed import made the
// sync report Success=true / Errors=0 while dropping the issue.
//
// Pure-Go (no cgo / embedded Dolt) so it runs under CGO_ENABLED=0 with the
// gms_pure_go build tag, like the other *_pure/*_dataloss helpers here.
package tracker

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
)

// createFailingStore fails CreateIssue for a new pulled issue so we can assert
// the failure is surfaced as stats.Errors, not silently dropped. GetIssueByExternalRef
// returns nil so the pull takes the create (not update) branch.
type createFailingStore struct {
	*pureTestStore
	createCalls int
}

func (s *createFailingStore) GetIssueByExternalRef(_ context.Context, _ string) (*types.Issue, error) {
	return nil, nil
}

func (s *createFailingStore) CreateIssue(_ context.Context, _ *types.Issue, _ string) error {
	s.createCalls++
	return errors.New("simulated write failure (disk-full / constraint)")
}

// TestEnginePullCountsCreateFailureAsError is the core beads-y26i guard: a
// CreateIssue failure during pull must be counted in stats.Errors, not
// swallowed into a clean-looking success.
func TestEnginePullCountsCreateFailureAsError(t *testing.T) {
	ctx := context.Background()

	store := &createFailingStore{pureTestStore: newPureTestStore()}

	tracker := newMockTracker("test")
	tracker.issues = []TrackerIssue{{
		ID:         "EXT-1",
		Identifier: "EXT-1",
		URL:        "https://test.test/EXT-1",
		Title:      "New remote issue",
		UpdatedAt:  time.Now(),
	}}

	engine := NewEngine(tracker, store, "test-actor")

	result, err := engine.Sync(ctx, SyncOptions{Pull: true})
	if err != nil {
		t.Fatalf("Sync() error: %v", err)
	}

	if store.createCalls == 0 {
		t.Fatalf("test setup: CreateIssue was never attempted")
	}
	if result.Stats.Created != 0 {
		t.Errorf("Stats.Created = %d, want 0 (the write failed)", result.Stats.Created)
	}
	// The bug: the failed create was neither Created, Skipped, nor Errors —
	// it vanished. It must be surfaced as an error.
	if result.Stats.Errors == 0 {
		t.Errorf("Stats.Errors = 0 but a CreateIssue failed during pull — the "+
			"write failure was silently swallowed (Created=%d Skipped=%d Errors=%d)",
			result.Stats.Created, result.Stats.Skipped, result.Stats.Errors)
	}
}

// updateFailingStore fails the transactional UpdateIssue used by doPull's
// existing-issue branch. GetIssueByExternalRef returns the local issue so the
// pull takes the update path; the external change is newer so an update is
// actually attempted.
type updateFailingStore struct {
	*pureTestStore
	updateCalls int
}

func (s *updateFailingStore) GetIssueByExternalRef(_ context.Context, externalRef string) (*types.Issue, error) {
	for _, issue := range s.issues {
		if issue.ExternalRef != nil && *issue.ExternalRef == externalRef {
			return issue, nil
		}
	}
	return nil, nil
}

func (s *updateFailingStore) RunInTransaction(ctx context.Context, _ string, fn func(tx storage.Transaction) error) error {
	return fn(&updateFailingTx{store: s})
}

type updateFailingTx struct {
	storage.Transaction
	store *updateFailingStore
}

func (t *updateFailingTx) UpdateIssue(_ context.Context, _ string, _ map[string]interface{}, _ string) error {
	t.store.updateCalls++
	return errors.New("simulated update failure")
}

func (t *updateFailingTx) GetLabels(_ context.Context, _ string) ([]string, error) { return nil, nil }

// TestEnginePullCountsUpdateFailureAsError mirrors the create case for the
// existing-issue update branch.
func TestEnginePullCountsUpdateFailureAsError(t *testing.T) {
	ctx := context.Background()

	extRef := "https://test.test/EXT-1"
	local := &types.Issue{
		ID:          "bd-1",
		Title:       "Old title",
		Description: "old",
		Status:      types.StatusOpen,
		IssueType:   types.TypeTask,
		Priority:    2,
		ExternalRef: strPtr(extRef),
		// Not locally modified (well before last_sync), so the pull applies the
		// remote change rather than skipping it as a local edit.
		UpdatedAt: time.Now().Add(-time.Hour),
	}
	store := &updateFailingStore{pureTestStore: newPureTestStore(local)}

	tracker := newMockTracker("test")
	tracker.issues = []TrackerIssue{{
		ID:         "EXT-1",
		Identifier: "EXT-1",
		URL:        extRef,
		Title:      "New title from remote",
		UpdatedAt:  time.Now(),
	}}

	engine := NewEngine(tracker, store, "test-actor")

	result, err := engine.Sync(ctx, SyncOptions{Pull: true})
	if err != nil {
		t.Fatalf("Sync() error: %v", err)
	}

	if store.updateCalls == 0 {
		t.Fatalf("test setup: UpdateIssue was never attempted")
	}
	if result.Stats.Errors == 0 {
		t.Errorf("Stats.Errors = 0 but an UpdateIssue failed during pull — the "+
			"write failure was silently swallowed (Created=%d Updated=%d Skipped=%d Errors=%d)",
			result.Stats.Created, result.Stats.Updated, result.Stats.Skipped, result.Stats.Errors)
	}
}
