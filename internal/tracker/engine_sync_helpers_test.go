// Pure-Go unit tests for the engine sync helpers that carried sub-80%
// coverage (beads-11en, agentic-tdd under beads-r06): resolveConflicts (0%),
// reimportIssue (0%), createDependencies (68%), and previewDependencies (75%).
//
// This file MUST NOT carry a `//go:build cgo` tag — it reuses the pure-Go
// helpers (mockTracker, mockMapper, pureTestStore) and adds a small
// dependency-capturing store, so everything runs under CGO_ENABLED=0 with the
// gms_pure_go build tag. No sql.DB, network, or embedded Dolt.

package tracker

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
)

// depCaptureStore extends pureTestStore with dependency create/read so
// createDependencies and dependencyExists can be exercised in-memory.
type depCaptureStore struct {
	*pureTestStore
	added        []*types.Dependency
	addErr       error
	existing     map[string][]*types.IssueWithDependencyMetadata
	getDepErr    error
	byExternal   map[string]*types.Issue // for GetIssueByExternalRef ("://" path)
	getExtRefErr error
}

func newDepCaptureStore(issues ...*types.Issue) *depCaptureStore {
	return &depCaptureStore{
		pureTestStore: newPureTestStore(issues...),
		existing:      make(map[string][]*types.IssueWithDependencyMetadata),
		byExternal:    make(map[string]*types.Issue),
	}
}

func (s *depCaptureStore) AddDependency(_ context.Context, dep *types.Dependency, _ string) error {
	if s.addErr != nil {
		return s.addErr
	}
	s.added = append(s.added, dep)
	return nil
}

func (s *depCaptureStore) GetDependenciesWithMetadata(_ context.Context, issueID string) ([]*types.IssueWithDependencyMetadata, error) {
	if s.getDepErr != nil {
		return nil, s.getDepErr
	}
	return s.existing[issueID], nil
}

func (s *depCaptureStore) GetIssueByExternalRef(_ context.Context, ref string) (*types.Issue, error) {
	if s.getExtRefErr != nil {
		return nil, s.getExtRefErr
	}
	return s.byExternal[ref], nil
}

func refPtr(s string) *string { return &s }

// --- resolveConflicts (0% → all four resolution arms) ---

func TestEngineResolveConflicts(t *testing.T) {
	older := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	newer := time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name         string
		resolution   ConflictResolution
		conflict     Conflict
		wantForce    bool
		wantSkip     bool
		wantPullOver bool
	}{
		{
			name:       "local keeps local (force push)",
			resolution: ConflictLocal,
			conflict:   Conflict{IssueID: "a"},
			wantForce:  true,
		},
		{
			name:         "external keeps external (skip + pull-overwrite)",
			resolution:   ConflictExternal,
			conflict:     Conflict{IssueID: "b"},
			wantSkip:     true,
			wantPullOver: true,
		},
		{
			name:       "timestamp local newer pushes",
			resolution: ConflictTimestamp,
			conflict:   Conflict{IssueID: "c", LocalUpdated: newer, ExternalUpdated: older},
			wantForce:  true,
		},
		{
			name:         "timestamp external newer imports",
			resolution:   ConflictTimestamp,
			conflict:     Conflict{IssueID: "d", LocalUpdated: older, ExternalUpdated: newer},
			wantSkip:     true,
			wantPullOver: true,
		},
		{
			name:         "unset resolution falls to timestamp default (external newer)",
			resolution:   "",
			conflict:     Conflict{IssueID: "e", LocalUpdated: older, ExternalUpdated: newer},
			wantSkip:     true,
			wantPullOver: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var msgs []string
			e := &Engine{OnMessage: func(s string) { msgs = append(msgs, s) }}
			skipIDs := map[string]bool{}
			forceIDs := map[string]bool{}
			allowPullOverwriteIDs := map[string]bool{}

			e.resolveConflicts(
				SyncOptions{ConflictResolution: tt.resolution},
				[]Conflict{tt.conflict},
				skipIDs, forceIDs, allowPullOverwriteIDs,
			)

			id := tt.conflict.IssueID
			if got := forceIDs[id]; got != tt.wantForce {
				t.Errorf("forceIDs[%q] = %v, want %v", id, got, tt.wantForce)
			}
			if got := skipIDs[id]; got != tt.wantSkip {
				t.Errorf("skipIDs[%q] = %v, want %v", id, got, tt.wantSkip)
			}
			if got := allowPullOverwriteIDs[id]; got != tt.wantPullOver {
				t.Errorf("allowPullOverwriteIDs[%q] = %v, want %v", id, got, tt.wantPullOver)
			}
			if len(msgs) != 1 {
				t.Errorf("expected exactly one conflict message, got %d: %v", len(msgs), msgs)
			}
		})
	}
}

func TestEngineResolveConflictsEmptyList(t *testing.T) {
	e := &Engine{}
	forceIDs := map[string]bool{}
	// Must not panic or mutate anything on an empty conflict slice.
	e.resolveConflicts(SyncOptions{}, nil, map[string]bool{}, forceIDs, map[string]bool{})
	if len(forceIDs) != 0 {
		t.Fatalf("forceIDs mutated on empty conflicts: %v", forceIDs)
	}
}

// --- reimportIssue (0% → fetch-err, nil conv, update-err, happy path) ---

func TestEngineReimportIssueFetchError(t *testing.T) {
	tr := newMockTracker("t")
	tr.fetchIssueErr = errors.New("boom")
	var warnings []string
	e := &Engine{Tracker: tr, OnWarning: func(s string) { warnings = append(warnings, s) }}

	e.reimportIssue(context.Background(), Conflict{IssueID: "x", ExternalIdentifier: "EXT-1"})

	if len(warnings) != 1 {
		t.Fatalf("expected one warning on fetch error, got %v", warnings)
	}
}

func TestEngineReimportIssueNilExternal(t *testing.T) {
	tr := newMockTracker("t")
	// No issues configured → FetchIssue returns (nil, nil) for any identifier.
	var warnings []string
	e := &Engine{Tracker: tr, OnWarning: func(s string) { warnings = append(warnings, s) }}

	e.reimportIssue(context.Background(), Conflict{IssueID: "x", ExternalIdentifier: "EXT-missing"})

	if len(warnings) != 1 {
		t.Fatalf("expected one warning on nil external issue, got %v", warnings)
	}
}

func TestEngineReimportIssueNilConversion(t *testing.T) {
	tr := newMockTracker("t")
	tr.issues = []TrackerIssue{{Identifier: "EXT-1", Title: "hello"}}
	// Mapper returns nil conversion → early return, no store update, no warning.
	tr.fieldMapper = &mockMapper{issueToBeads: func(*TrackerIssue) *IssueConversion { return nil }}
	store := newPureTestStore(&types.Issue{ID: "x"})
	var warnings []string
	e := &Engine{Tracker: tr, Store: store, OnWarning: func(s string) { warnings = append(warnings, s) }}

	e.reimportIssue(context.Background(), Conflict{IssueID: "x", ExternalIdentifier: "EXT-1"})

	if len(warnings) != 0 {
		t.Fatalf("expected no warning on nil conversion, got %v", warnings)
	}
}

// reimportUpdateErrStore fails UpdateIssue to exercise the update-error branch.
type reimportUpdateErrStore struct {
	*pureTestStore
}

func (s *reimportUpdateErrStore) UpdateIssue(_ context.Context, _ string, _ map[string]interface{}, _ string) error {
	return errors.New("update failed")
}

func TestEngineReimportIssueUpdateError(t *testing.T) {
	tr := newMockTracker("t")
	tr.issues = []TrackerIssue{{
		Identifier: "EXT-1",
		Title:      "title",
		Metadata:   map[string]interface{}{"k": "v"},
	}}
	store := &reimportUpdateErrStore{pureTestStore: newPureTestStore()}
	var warnings []string
	e := &Engine{Tracker: tr, Store: store, OnWarning: func(s string) { warnings = append(warnings, s) }}

	e.reimportIssue(context.Background(), Conflict{IssueID: "x", ExternalIdentifier: "EXT-1"})

	if len(warnings) != 1 {
		t.Fatalf("expected one warning on update error, got %v", warnings)
	}
}

// reimportCaptureStore records the update map so the happy path can be asserted.
type reimportCaptureStore struct {
	*pureTestStore
	gotID      string
	gotUpdates map[string]interface{}
}

func (s *reimportCaptureStore) UpdateIssue(_ context.Context, id string, updates map[string]interface{}, _ string) error {
	s.gotID = id
	s.gotUpdates = updates
	return nil
}

func TestEngineReimportIssueHappyPath(t *testing.T) {
	tr := newMockTracker("t")
	tr.issues = []TrackerIssue{{
		Identifier: "EXT-1",
		Title:      "new title",
		Metadata:   map[string]interface{}{"labels": []string{"z"}},
	}}
	store := &reimportCaptureStore{pureTestStore: newPureTestStore()}
	e := &Engine{Tracker: tr, Store: store}

	e.reimportIssue(context.Background(), Conflict{IssueID: "beads-1", ExternalIdentifier: "EXT-1"})

	if store.gotID != "beads-1" {
		t.Fatalf("UpdateIssue id = %q, want beads-1", store.gotID)
	}
	if store.gotUpdates["title"] != "new title" {
		t.Errorf("update title = %v, want 'new title'", store.gotUpdates["title"])
	}
	if _, ok := store.gotUpdates["metadata"]; !ok {
		t.Errorf("expected metadata key in updates, got %v", store.gotUpdates)
	}
}

// --- createDependencies (68% → resolver-err, resolve-err, nil-issue, add-err, happy) ---

// streamErrStore makes IterIssues fail so dependencyIssueResolver errors out.
type streamErrStore struct {
	*pureTestStore
}

func (s *streamErrStore) IterIssues(_ context.Context, _ string, _ types.IssueFilter) (storage.Iter[types.Issue], error) {
	return nil, errors.New("iter failed")
}

func TestEngineCreateDependenciesEmpty(t *testing.T) {
	e := &Engine{}
	if got := e.createDependencies(context.Background(), nil); got != 0 {
		t.Fatalf("createDependencies(nil) = %d, want 0", got)
	}
}

func TestEngineCreateDependenciesResolverBuildError(t *testing.T) {
	tr := newMockTracker("t")
	store := &streamErrStore{pureTestStore: newPureTestStore()}
	var warnings []string
	e := &Engine{Tracker: tr, Store: store, OnWarning: func(s string) { warnings = append(warnings, s) }}

	deps := []DependencyInfo{{FromExternalID: "EXT-1", ToExternalID: "EXT-2", Type: "blocks"}}
	if got := e.createDependencies(context.Background(), deps); got != len(deps) {
		t.Fatalf("createDependencies with resolver-build error = %d, want %d", got, len(deps))
	}
	if len(warnings) != 1 {
		t.Fatalf("expected one warning, got %v", warnings)
	}
}

func TestEngineCreateDependenciesUnresolvedSource(t *testing.T) {
	tr := newMockTracker("t")
	// No local issues → resolver returns (nil, nil) for the source; nil issue,
	// no error → "continue" (not counted as an error).
	store := newDepCaptureStore()
	e := &Engine{Tracker: tr, Store: store}

	deps := []DependencyInfo{{FromExternalID: "EXT-nope", ToExternalID: "EXT-2", Type: "blocks"}}
	if got := e.createDependencies(context.Background(), deps); got != 0 {
		t.Fatalf("createDependencies unresolved source = %d, want 0", got)
	}
	if len(store.added) != 0 {
		t.Fatalf("no dependency should be created, got %v", store.added)
	}
}

func TestEngineCreateDependenciesHappyAndAddError(t *testing.T) {
	from := &types.Issue{ID: "beads-from", ExternalRef: refPtr("EXT-1")}
	to := &types.Issue{ID: "beads-to", ExternalRef: refPtr("EXT-2")}

	tr := newMockTracker("t")
	deps := []DependencyInfo{{FromExternalID: "EXT-1", ToExternalID: "EXT-2", Type: "blocks"}}

	t.Run("happy path creates dependency", func(t *testing.T) {
		store := newDepCaptureStore(from, to)
		e := &Engine{Tracker: tr, Store: store}
		if got := e.createDependencies(context.Background(), deps); got != 0 {
			t.Fatalf("createDependencies happy = %d, want 0", got)
		}
		if len(store.added) != 1 {
			t.Fatalf("expected 1 dependency created, got %d", len(store.added))
		}
		if store.added[0].IssueID != "beads-from" || store.added[0].DependsOnID != "beads-to" {
			t.Errorf("dependency = %+v, want beads-from -> beads-to", store.added[0])
		}
	})

	t.Run("add error is counted", func(t *testing.T) {
		store := newDepCaptureStore(from, to)
		store.addErr = errors.New("add failed")
		var warnings []string
		e := &Engine{Tracker: tr, Store: store, OnWarning: func(s string) { warnings = append(warnings, s) }}
		if got := e.createDependencies(context.Background(), deps); got != 1 {
			t.Fatalf("createDependencies add-error = %d, want 1", got)
		}
		if len(warnings) != 1 {
			t.Fatalf("expected one warning on add error, got %v", warnings)
		}
	})
}

// --- previewDependencies (75% → resolver-err, dry-run would-create, dedup) ---

func TestEnginePreviewDependenciesEmpty(t *testing.T) {
	e := &Engine{}
	if got := e.previewDependencies(context.Background(), nil, nil); got != 0 {
		t.Fatalf("previewDependencies(nil) = %d, want 0", got)
	}
}

func TestEnginePreviewDependenciesResolverBuildError(t *testing.T) {
	tr := newMockTracker("t")
	store := &streamErrStore{pureTestStore: newPureTestStore()}
	var warnings []string
	e := &Engine{Tracker: tr, Store: store, OnWarning: func(s string) { warnings = append(warnings, s) }}

	deps := []DependencyInfo{{FromExternalID: "EXT-1", ToExternalID: "EXT-2", Type: "blocks"}}
	if got := e.previewDependencies(context.Background(), deps, nil); got != len(deps) {
		t.Fatalf("previewDependencies resolver-build error = %d, want %d", got, len(deps))
	}
	if len(warnings) != 1 {
		t.Fatalf("expected one warning, got %v", warnings)
	}
}

func TestEnginePreviewDependenciesDryRunAndDedup(t *testing.T) {
	from := &types.Issue{ID: "beads-from", ExternalRef: refPtr("EXT-1")}
	to := &types.Issue{ID: "beads-to", ExternalRef: refPtr("EXT-2")}
	tr := newMockTracker("t")
	store := newDepCaptureStore()
	var msgs []string
	e := &Engine{Tracker: tr, Store: store, OnMessage: func(s string) { msgs = append(msgs, s) }}

	// Two identical deps → deduped to a single "would create" and never persisted.
	deps := []DependencyInfo{
		{FromExternalID: "EXT-1", ToExternalID: "EXT-2", Type: "blocks"},
		{FromExternalID: "EXT-1", ToExternalID: "EXT-2", Type: "blocks"},
	}
	// dryRunIssues carries the local issues via the resolver's extraIssues arg.
	if got := e.previewDependencies(context.Background(), deps, []*types.Issue{from, to}); got != 0 {
		t.Fatalf("previewDependencies always returns 0, got %d", got)
	}
	if len(store.added) != 0 {
		t.Fatalf("dry-run must not persist dependencies, got %v", store.added)
	}
	// Expect one "Would create dependency" line + one summary line.
	var wouldCreate int
	for _, m := range msgs {
		if len(m) >= 9 && m[:9] == "[dry-run]" {
			wouldCreate++
		}
	}
	if wouldCreate != 2 {
		t.Fatalf("expected 2 dry-run messages (one dep + summary), got %d: %v", wouldCreate, msgs)
	}
}

func TestEnginePreviewDependenciesExistingSkipped(t *testing.T) {
	from := &types.Issue{ID: "beads-from", ExternalRef: refPtr("EXT-1")}
	to := &types.Issue{ID: "beads-to", ExternalRef: refPtr("EXT-2")}
	tr := newMockTracker("t")
	store := newDepCaptureStore()
	// Mark the dependency as already existing → previewDependencies skips it.
	store.existing["beads-from"] = []*types.IssueWithDependencyMetadata{
		func() *types.IssueWithDependencyMetadata {
			m := &types.IssueWithDependencyMetadata{DependencyType: types.DependencyType("blocks")}
			m.Issue.ID = "beads-to"
			return m
		}(),
	}
	var msgs []string
	e := &Engine{Tracker: tr, Store: store, OnMessage: func(s string) { msgs = append(msgs, s) }}

	deps := []DependencyInfo{{FromExternalID: "EXT-1", ToExternalID: "EXT-2", Type: "blocks"}}
	e.previewDependencies(context.Background(), deps, []*types.Issue{from, to})

	for _, m := range msgs {
		if len(m) >= 9 && m[:9] == "[dry-run]" {
			t.Fatalf("existing dependency should not produce a dry-run message, got %v", msgs)
		}
	}
}
