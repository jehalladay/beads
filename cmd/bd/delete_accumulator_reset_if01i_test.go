package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
)

// beads-if01i: the single-force delete path (cmd/bd/delete.go) declared
// updatedIssueCount/totalDepsRemoved OUTSIDE the transactHonoringAutoCommit
// closure and incremented them INSIDE it. transactHonoringAutoCommit →
// DoltStore.RunInTransaction → withRetry re-invokes the closure on any
// retryable error (serialization conflict 1213/1205, pre-commit connection
// blip) from a rolled-back state, so a retried attempt added its increments on
// top of the last: the reported "Removed N dependency link(s)" / "Updated text
// references in N issue(s)" (human + --json dependencies_removed /
// references_updated) inflated even though the DB state was correct (the SQL tx
// rolls back per attempt). Same class as t0h3z (burn/squash/batch); the
// single-delete leg was out of its scope. The fix resets both accumulators at
// closure entry (last-attempt-wins, whole closure re-runs).
//
// This drives the REAL deleteCmd.RunE (--json) against a fake store whose
// RunInTransaction invokes the closure TWICE (one retry) then succeeds, and
// asserts the reported counts equal the single-attempt truth (not doubled).
// Pure-Go (no cgo / no embedded Dolt): the fake tx accepts writes as no-ops —
// only the accumulator freshness is asserted, not DB state.

// delRetryFakeStore is a storage.DoltStorage whose RunInTransaction invokes the
// closure `attempts` times (simulating withRetry re-invocations). The embedded
// nil interface satisfies the rest of DoltStorage; the delete RunE pre-closure
// path only calls SearchIssues/GetConfig/GetIssue/GetDependencies/GetDependents/
// GetDependencyRecords, which this fake implements.
type delRetryFakeStore struct {
	storage.DoltStorage
	attempts   int
	target     *types.Issue
	connected  []*types.Issue      // issues with text refs to target (dependents)
	depRecords []*types.Dependency // outbound dependency records of target
	dependents []*types.Issue      // inbound dependents of target
}

func (s *delRetryFakeStore) SearchIssues(_ context.Context, _ string, filter types.IssueFilter) ([]*types.Issue, error) {
	for _, id := range filter.IDs {
		if id == s.target.ID {
			return []*types.Issue{s.target}, nil
		}
	}
	return nil, nil
}

func (s *delRetryFakeStore) GetConfig(_ context.Context, _ string) (string, error) {
	return "", storage.ErrNotFound
}

func (s *delRetryFakeStore) GetIssue(_ context.Context, id string) (*types.Issue, error) {
	if id == s.target.ID {
		return s.target, nil
	}
	return nil, nil
}

func (s *delRetryFakeStore) GetDependencies(_ context.Context, _ string) ([]*types.Issue, error) {
	return nil, nil
}

func (s *delRetryFakeStore) GetDependents(_ context.Context, _ string) ([]*types.Issue, error) {
	return s.dependents, nil
}

func (s *delRetryFakeStore) GetDependencyRecords(_ context.Context, _ string) ([]*types.Dependency, error) {
	return s.depRecords, nil
}

// beads-qh4jx: the single-force delete RunE now reads labels + events on the
// target (to report labels_removed/events_removed in the --json/text output,
// matching the batch path). Without these no-op overrides the call would
// dispatch to the embedded nil storage.DoltStorage and SIGSEGV (the embedded-
// nil-interface-fake panic class — see y5saa). Return empty: this test asserts
// only accumulator-reset freshness (dependencies_removed/references_updated),
// not label/event counts.
func (s *delRetryFakeStore) GetLabels(_ context.Context, _ string) ([]string, error) {
	return nil, nil
}

func (s *delRetryFakeStore) GetEvents(_ context.Context, _ string, _ int) ([]*types.Event, error) {
	return nil, nil
}

// beads-au6dv: the delete flow now rewrites id refs inside connected issues'
// comment bodies (rewriteCommentRefs) as a post-tx follow pass. This accumulator
// mock has no comments, so return an empty set (and a no-op update) rather than
// nil-panic through the embedded nil DoltStorage.
func (s *delRetryFakeStore) GetIssueComments(_ context.Context, _ string) ([]*types.Comment, error) {
	return nil, nil
}

func (s *delRetryFakeStore) UpdateCommentText(_ context.Context, _, _, _ string) error {
	return nil
}

func (s *delRetryFakeStore) RunInTransaction(_ context.Context, _ string, fn func(storage.Transaction) error) error {
	n := s.attempts
	if n < 1 {
		n = 1
	}
	var err error
	for i := 0; i < n; i++ {
		tx := &delRetryFakeTx{}
		if err = fn(tx); err != nil {
			return err
		}
	}
	return nil
}

// delRetryFakeTx accepts every write as a no-op; only the accumulator freshness
// is under test, not the DB state (which the real SQL tx would roll back per
// attempt anyway).
type delRetryFakeTx struct {
	storage.Transaction
}

func (tx *delRetryFakeTx) UpdateIssue(_ context.Context, _ string, _ map[string]interface{}, _ string) error {
	return nil
}

func (tx *delRetryFakeTx) RemoveDependency(_ context.Context, _, _ string, _ string) error {
	return nil
}

func (tx *delRetryFakeTx) DeleteIssue(_ context.Context, _ string) error { return nil }

func TestDeleteSingleForceAccumulatorResetsOnRetry_if01i(t *testing.T) {
	// target bd-t has one outbound dep record (bd-t → bd-d) and one inbound
	// dependent (bd-r → bd-t). bd-r's title references bd-t, so the force path
	// rewrites it (references_updated++) AND removes both edges
	// (dependencies_removed += 2). With a double-invoking RunInTransaction the
	// pre-fix code reported references_updated=2, dependencies_removed=4.
	target := &types.Issue{ID: "bd-t", Title: "target", Status: types.StatusOpen, Priority: 2}
	referer := &types.Issue{ID: "bd-r", Title: "refers to bd-t", Status: types.StatusOpen, Priority: 2}

	s := &delRetryFakeStore{
		attempts:   2,
		target:     target,
		dependents: []*types.Issue{referer},
		depRecords: []*types.Dependency{{IssueID: "bd-t", DependsOnID: "bd-d", Type: types.DepBlocks}},
	}

	oldStore, oldCtx, oldJSON, oldReadonly := store, rootCtx, jsonOutput, readonlyMode
	store = s
	rootCtx = context.Background()
	jsonOutput = true
	readonlyMode = false
	t.Cleanup(func() {
		store, rootCtx, jsonOutput, readonlyMode = oldStore, oldCtx, oldJSON, oldReadonly
	})

	// force=true so the actual (retry-wrapped) delete transaction runs.
	if err := deleteCmd.Flags().Set("force", "true"); err != nil {
		t.Fatalf("set --force: %v", err)
	}
	t.Cleanup(func() { _ = deleteCmd.Flags().Set("force", "false") })

	out := captureStdout(t, func() error {
		return deleteCmd.RunE(deleteCmd, []string{"bd-t"})
	})

	var env struct {
		DependenciesRemoved int `json:"dependencies_removed"`
		ReferencesUpdated   int `json:"references_updated"`
	}
	// The --json output is schema-version-wrapped; decode leniently by scanning
	// for the payload keys via a generic map.
	var generic map[string]any
	dec := json.NewDecoder(strings.NewReader(out))
	if err := dec.Decode(&generic); err != nil {
		t.Fatalf("decode --json output: %v\noutput:\n%s", err, out)
	}
	env.DependenciesRemoved = intField(generic, "dependencies_removed")
	env.ReferencesUpdated = intField(generic, "references_updated")

	// One outbound + one inbound edge removed = 2; one referer title rewritten = 1.
	if env.DependenciesRemoved != 2 {
		t.Errorf("dependencies_removed = %d after one retry, want 2 (accumulator not reset per attempt)\noutput:\n%s",
			env.DependenciesRemoved, out)
	}
	if env.ReferencesUpdated != 1 {
		t.Errorf("references_updated = %d after one retry, want 1 (accumulator not reset per attempt)\noutput:\n%s",
			env.ReferencesUpdated, out)
	}
	if env.DependenciesRemoved == 4 || env.ReferencesUpdated == 2 {
		t.Errorf("delete double-counted on tx retry (accumulator not reset): deps=%d refs=%d\noutput:\n%s",
			env.DependenciesRemoved, env.ReferencesUpdated, out)
	}
}

// intField reads an int from a possibly-wrapped generic JSON map, tolerating
// the schema-version envelope by checking the top level and, if absent, a
// nested payload.
func intField(m map[string]any, key string) int {
	if v, ok := m[key]; ok {
		if f, ok := v.(float64); ok {
			return int(f)
		}
	}
	for _, v := range m {
		if nested, ok := v.(map[string]any); ok {
			if f, ok := nested[key].(float64); ok {
				return int(f)
			}
		}
	}
	return -1
}
