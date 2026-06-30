// Regression tests for silent sync data-loss (beads-r06.14).
//
// These cover two audit findings in engine.go:
//
//	#18 — Sync() persists the new last_sync cursor via SetLocalMetadata. If that
//	      write fails it was only e.warn-logged and Sync still reported success,
//	      so the next sync re-evaluated conflicts against a stale cursor and
//	      could silently overwrite local edits. The failure must be propagated.
//
//	#19 — The "modified since last_sync" boundary was asymmetric across the two
//	      guards. DetectConflicts treated UpdatedAt == lastSync as NOT modified
//	      (no conflict), while doPull only protected issues strictly After
//	      lastSync — so a local edit landing exactly at lastSync was neither
//	      flagged as a conflict nor protected from overwrite, and was silently
//	      clobbered by the incoming remote version. Both guards must agree that
//	      UpdatedAt >= lastSync means "locally modified".
//
// This file MUST stay pure-Go (no cgo / embedded Dolt) so it runs under
// CGO_ENABLED=0 with the gms_pure_go build tag, like the other *_pure_test.go
// helpers in this package.
package tracker

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
)

// failingMetadataStore wraps pureTestStore and fails SetLocalMetadata so we can
// assert that a failed last_sync persist is surfaced rather than swallowed.
type failingMetadataStore struct {
	*pureTestStore
	setErr   error
	setCalls int
}

func (s *failingMetadataStore) SetLocalMetadata(_ context.Context, _, _ string) error {
	s.setCalls++
	return s.setErr
}

// #18: a failed last_sync write must not be reported as a successful sync.
func TestEngineSyncPropagatesLastSyncWriteFailure(t *testing.T) {
	ctx := context.Background()
	store := &failingMetadataStore{
		pureTestStore: newPureTestStore(),
		setErr:        errors.New("dolt: metadata write rejected"),
	}

	tracker := newMockTracker("test")
	engine := NewEngine(tracker, store, "test-actor")

	result, err := engine.Sync(ctx, SyncOptions{Pull: true, Push: true})

	if store.setCalls == 0 {
		t.Fatalf("expected Sync to attempt the last_sync write")
	}
	if err == nil {
		t.Fatalf("expected Sync to return an error when last_sync write fails, got nil (result=%+v)", result)
	}
	if result != nil && result.Success {
		t.Fatalf("expected result.Success=false when last_sync write fails, got Success=true")
	}
}

// pullTestStore extends pureTestStore with the surface doPull touches when it
// applies an update to an existing issue: external-ref lookup and a real
// transaction that records UpdateIssue calls. It lets us observe whether a
// local edit was overwritten.
type pullTestStore struct {
	*pureTestStore
	updatedIDs []string
}

func (s *pullTestStore) GetIssueByExternalRef(_ context.Context, externalRef string) (*types.Issue, error) {
	for _, issue := range s.issues {
		if issue.ExternalRef != nil && *issue.ExternalRef == externalRef {
			return issue, nil
		}
	}
	return nil, nil
}

func (s *pullTestStore) RunInTransaction(ctx context.Context, _ string, fn func(tx storage.Transaction) error) error {
	return fn(&pullTestTx{store: s})
}

// pullTestTx is a minimal Transaction recording the writes doPull performs.
type pullTestTx struct {
	storage.Transaction
	store *pullTestStore
}

func (t *pullTestTx) UpdateIssue(_ context.Context, id string, updates map[string]interface{}, _ string) error {
	t.store.updatedIDs = append(t.store.updatedIDs, id)
	for _, issue := range t.store.issues {
		if issue.ID != id {
			continue
		}
		if v, ok := updates["title"].(string); ok {
			issue.Title = v
		}
		if v, ok := updates["description"].(string); ok {
			issue.Description = v
		}
	}
	return nil
}

func (t *pullTestTx) GetLabels(_ context.Context, _ string) ([]string, error) { return nil, nil }

// #19: a local edit whose UpdatedAt is exactly equal to last_sync must be
// treated as locally modified and NOT silently overwritten by an incoming pull.
func TestEnginePullDoesNotOverwriteLocalEditAtLastSyncBoundary(t *testing.T) {
	ctx := context.Background()

	// last_sync recorded at a whole second, matching the engine's own cursor
	// rounding (see Sync: Truncate(Second).Add(Second)).
	lastSync := time.Now().UTC().Truncate(time.Second)
	extRef := "https://test.test/EXT-1"

	local := &types.Issue{
		ID:          "bd-boundary",
		Title:       "Local edit",
		Description: "Local description",
		Status:      types.StatusOpen,
		IssueType:   types.TypeTask,
		Priority:    2,
		ExternalRef: strPtr(extRef),
		// Locally modified at EXACTLY last_sync — the asymmetric boundary case.
		UpdatedAt: lastSync,
	}

	store := &pullTestStore{pureTestStore: newPureTestStore(local)}
	if err := store.SetLocalMetadata(ctx, "test.last_sync", lastSync.Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("SetLocalMetadata() error: %v", err)
	}

	tracker := newMockTracker("test")
	tracker.issues = []TrackerIssue{{
		ID:          "EXT-1",
		Identifier:  "EXT-1",
		URL:         extRef,
		Title:       "Remote edit",
		Description: "Remote description",
		UpdatedAt:   lastSync.Add(30 * time.Minute),
	}}

	engine := NewEngine(tracker, store, "test-actor")

	// Pull only: conflict detection (the Sync phase-1 path) is bypassed, so
	// doPull's own guard is the sole protection for the local edit.
	if _, err := engine.Sync(ctx, SyncOptions{Pull: true}); err != nil {
		t.Fatalf("Sync() error: %v", err)
	}

	if len(store.updatedIDs) != 0 {
		t.Fatalf("local edit at last_sync boundary was overwritten (updatedIDs=%v), want it preserved", store.updatedIDs)
	}
	if local.Title != "Local edit" || local.Description != "Local description" {
		t.Fatalf("local edit clobbered: title=%q description=%q", local.Title, local.Description)
	}
}

// #19 (DetectConflicts side): an external + local edit, where the local
// UpdatedAt equals last_sync, must be reported as a conflict rather than
// silently ignored.
func TestEngineDetectConflictsAtLastSyncBoundary(t *testing.T) {
	ctx := context.Background()

	lastSync := time.Now().UTC().Truncate(time.Second)
	extRef := "https://test.test/EXT-1"

	local := &types.Issue{
		ID:          "bd-boundary",
		Title:       "Local edit",
		Status:      types.StatusOpen,
		IssueType:   types.TypeTask,
		Priority:    2,
		ExternalRef: strPtr(extRef),
		UpdatedAt:   lastSync, // exactly at the boundary
	}

	store := newPureTestStore(local)
	if err := store.SetLocalMetadata(ctx, "test.last_sync", lastSync.Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("SetLocalMetadata() error: %v", err)
	}

	tracker := newMockTracker("test")
	tracker.issues = []TrackerIssue{{
		ID:         "EXT-1",
		Identifier: "EXT-1",
		URL:        extRef,
		Title:      "Remote edit",
		UpdatedAt:  lastSync.Add(15 * time.Minute), // also changed externally
	}}

	engine := NewEngine(tracker, store, "test-actor")

	conflicts, err := engine.DetectConflicts(ctx)
	if err != nil {
		t.Fatalf("DetectConflicts() error: %v", err)
	}
	if len(conflicts) != 1 {
		t.Fatalf("detected %d conflicts at last_sync boundary, want 1", len(conflicts))
	}
}

// REGRESSION GUARD for the legitimate re-pull path: a row the engine itself
// wrote on the previous pull (no local edit) must still be updated when the
// remote later changes. The fix records last_sync at floor(now)+2s precisely so
// the engine's own writes — which Dolt rounds to at most floor(now)+1 — land
// STRICTLY below the cursor, and are therefore treated as "not locally
// modified" by the >= guard. This is the case the +2s margin protects against:
// without it, a naive >= guard would treat the engine's own boundary write as a
// local edit and skip the remote update, silently leaving stale data.
func TestEnginePullAppliesRemoteUpdateForEnginesOwnPriorWrite(t *testing.T) {
	ctx := context.Background()

	// last_sync is the cursor; the engine's own prior write is recorded one
	// second below it (the worst case the +2s margin must still keep below).
	lastSync := time.Now().UTC().Truncate(time.Second)
	extRef := "https://test.test/EXT-1"

	local := &types.Issue{
		ID:          "bd-enginewrite",
		Title:       "Remote v1",
		Description: "Remote description v1",
		Status:      types.StatusOpen,
		IssueType:   types.TypeTask,
		Priority:    2,
		ExternalRef: strPtr(extRef),
		UpdatedAt:   lastSync.Add(-1 * time.Second), // engine's own write, below cursor
	}

	store := &pullTestStore{pureTestStore: newPureTestStore(local)}
	if err := store.SetLocalMetadata(ctx, "test.last_sync", lastSync.Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("SetLocalMetadata() error: %v", err)
	}

	tracker := newMockTracker("test")
	tracker.issues = []TrackerIssue{{
		ID:          "EXT-1",
		Identifier:  "EXT-1",
		URL:         extRef,
		Title:       "Remote v2", // remote changed since last pull
		Description: "Remote description v2",
		UpdatedAt:   lastSync.Add(30 * time.Minute),
	}}

	engine := NewEngine(tracker, store, "test-actor")
	if _, err := engine.Sync(ctx, SyncOptions{Pull: true}); err != nil {
		t.Fatalf("Sync() error: %v", err)
	}

	if len(store.updatedIDs) != 1 {
		t.Fatalf("engine's own prior-write row was not updated by a changed remote (updatedIDs=%v), want it pulled", store.updatedIDs)
	}
}

// REGRESSION GUARD for the cursor margin itself: a full Sync must record a
// last_sync cursor strictly greater than the rounded timestamp of any write it
// performed during that sync. We assert the recorded cursor is at least
// ceil(now)+1s into the future, which is what lets the >= conflict guards stay
// correct. (floor(now)+2s; >= now+1s for any sub-second now.)
func TestEngineSyncCursorIsStrictlyAheadOfEngineWrites(t *testing.T) {
	ctx := context.Background()
	store := newPureTestStore()
	tracker := newMockTracker("test")
	engine := NewEngine(tracker, store, "test-actor")

	before := time.Now().UTC()
	result, err := engine.Sync(ctx, SyncOptions{Pull: true, Push: true})
	if err != nil {
		t.Fatalf("Sync() error: %v", err)
	}
	if result.LastSync == "" {
		t.Fatalf("Sync recorded no last_sync cursor")
	}
	cursor, err := time.Parse(time.RFC3339Nano, result.LastSync)
	if err != nil {
		t.Fatalf("parsing recorded cursor %q: %v", result.LastSync, err)
	}
	// A write performed at wall time t (>= before) rounds in Dolt to at most
	// floor(t)+1. The cursor must exceed that for every t in [before, now].
	maxRoundedWrite := before.Truncate(time.Second).Add(time.Second)
	if !cursor.After(maxRoundedWrite) {
		t.Fatalf("cursor %s is not strictly after the worst-case rounded engine write %s", cursor, maxRoundedWrite)
	}
}
