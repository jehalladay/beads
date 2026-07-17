package telemetry

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
)

// recordingStorage is a complete storage.Storage implementation for exercising
// the InstrumentedStorage delegation layer. Every method records its name in
// lastCall and returns the configured err (and a zero-valued success result),
// so a single fake can drive both the success and error path of all 62
// wrappers without a live Dolt backend.
//
// It is NOT embedded (unlike fakeStorage) so that a compile error fires if the
// storage.Storage interface grows a method the wrapper layer must also cover —
// keeping this delegation matrix honest as the interface evolves.
type recordingStorage struct {
	lastCall string
	err      error
}

func (r *recordingStorage) rec(name string) error {
	r.lastCall = name
	return r.err
}

// ── Issue CRUD ──
func (r *recordingStorage) CreateIssue(context.Context, *types.Issue, string) error {
	return r.rec("CreateIssue")
}
func (r *recordingStorage) CreateIssues(context.Context, []*types.Issue, string) error {
	return r.rec("CreateIssues")
}
func (r *recordingStorage) GetIssue(context.Context, string) (*types.Issue, error) {
	return nil, r.rec("GetIssue")
}
func (r *recordingStorage) GetIssueByExternalRef(context.Context, string) (*types.Issue, error) {
	return nil, r.rec("GetIssueByExternalRef")
}
func (r *recordingStorage) GetIssuesByIDs(context.Context, []string) ([]*types.Issue, error) {
	return nil, r.rec("GetIssuesByIDs")
}
func (r *recordingStorage) UpdateIssue(context.Context, string, map[string]interface{}, string) error {
	return r.rec("UpdateIssue")
}
func (r *recordingStorage) ReopenIssue(context.Context, string, string, string) error {
	return r.rec("ReopenIssue")
}
func (r *recordingStorage) UpdateIssueType(context.Context, string, string, string) error {
	return r.rec("UpdateIssueType")
}
func (r *recordingStorage) CloseIssue(context.Context, string, string, string, string) error {
	return r.rec("CloseIssue")
}
func (r *recordingStorage) DeleteIssue(context.Context, string) error {
	return r.rec("DeleteIssue")
}
func (r *recordingStorage) SearchIssues(context.Context, string, types.IssueFilter) ([]*types.Issue, error) {
	return nil, r.rec("SearchIssues")
}
func (r *recordingStorage) SearchIssuesWithCounts(context.Context, string, types.IssueFilter) ([]*types.IssueWithCounts, error) {
	return nil, r.rec("SearchIssuesWithCounts")
}

// ── Dependencies ──
func (r *recordingStorage) AddDependency(context.Context, *types.Dependency, string) error {
	return r.rec("AddDependency")
}
func (r *recordingStorage) RemoveDependency(context.Context, string, string, string) error {
	return r.rec("RemoveDependency")
}
func (r *recordingStorage) GetDependencies(context.Context, string) ([]*types.Issue, error) {
	return nil, r.rec("GetDependencies")
}
func (r *recordingStorage) GetDependents(context.Context, string) ([]*types.Issue, error) {
	return nil, r.rec("GetDependents")
}
func (r *recordingStorage) GetDependenciesWithMetadata(context.Context, string) ([]*types.IssueWithDependencyMetadata, error) {
	return nil, r.rec("GetDependenciesWithMetadata")
}
func (r *recordingStorage) GetDependentsWithMetadata(context.Context, string) ([]*types.IssueWithDependencyMetadata, error) {
	return nil, r.rec("GetDependentsWithMetadata")
}
func (r *recordingStorage) GetDependencyTree(context.Context, string, int, bool, bool) ([]*types.TreeNode, error) {
	return nil, r.rec("GetDependencyTree")
}

// ── Labels ──
func (r *recordingStorage) AddLabel(context.Context, string, string, string) error {
	return r.rec("AddLabel")
}
func (r *recordingStorage) RemoveLabel(context.Context, string, string, string) error {
	return r.rec("RemoveLabel")
}
func (r *recordingStorage) GetLabels(context.Context, string) ([]string, error) {
	return nil, r.rec("GetLabels")
}
func (r *recordingStorage) GetIssuesByLabel(context.Context, string) ([]*types.Issue, error) {
	return nil, r.rec("GetIssuesByLabel")
}

// ── Work queries ──
func (r *recordingStorage) GetReadyWork(context.Context, types.WorkFilter) ([]*types.Issue, error) {
	return nil, r.rec("GetReadyWork")
}
func (r *recordingStorage) GetReadyWorkWithCounts(context.Context, types.WorkFilter) ([]*types.IssueWithCounts, error) {
	return nil, r.rec("GetReadyWorkWithCounts")
}
func (r *recordingStorage) GetBlockedIssues(context.Context, types.WorkFilter) ([]*types.BlockedIssue, error) {
	return nil, r.rec("GetBlockedIssues")
}
func (r *recordingStorage) GetEpicsEligibleForClosure(context.Context) ([]*types.EpicStatus, error) {
	return nil, r.rec("GetEpicsEligibleForClosure")
}

// ── Wisp queries ──
func (r *recordingStorage) ListWisps(context.Context, types.WispFilter) ([]*types.Issue, error) {
	return nil, r.rec("ListWisps")
}

// ── Comments & events ──
func (r *recordingStorage) AddIssueComment(context.Context, string, string, string) (*types.Comment, error) {
	return nil, r.rec("AddIssueComment")
}
func (r *recordingStorage) GetIssueComments(context.Context, string) ([]*types.Comment, error) {
	return nil, r.rec("GetIssueComments")
}
func (r *recordingStorage) GetEvents(context.Context, string, int) ([]*types.Event, error) {
	return nil, r.rec("GetEvents")
}
func (r *recordingStorage) GetAllEventsSince(context.Context, time.Time) ([]*types.Event, error) {
	return nil, r.rec("GetAllEventsSince")
}

// ── Aggregate counts ──
func (r *recordingStorage) CountIssues(context.Context, string, types.IssueFilter) (int64, error) {
	return 0, r.rec("CountIssues")
}
func (r *recordingStorage) CountIssuesByGroup(context.Context, types.IssueFilter, string) (map[string]int, error) {
	return nil, r.rec("CountIssuesByGroup")
}
func (r *recordingStorage) CountDependents(context.Context, string) (int64, error) {
	return 0, r.rec("CountDependents")
}
func (r *recordingStorage) CountDependencies(context.Context, string) (int64, error) {
	return 0, r.rec("CountDependencies")
}
func (r *recordingStorage) CountIssueComments(context.Context, string) (int64, error) {
	return 0, r.rec("CountIssueComments")
}
func (r *recordingStorage) CountEvents(context.Context, string, int) (int64, error) {
	return 0, r.rec("CountEvents")
}

// ── Streaming iterators ──
func (r *recordingStorage) IterIssues(context.Context, string, types.IssueFilter) (storage.Iter[types.Issue], error) {
	return nil, r.rec("IterIssues")
}
func (r *recordingStorage) IterDependentsWithMetadata(context.Context, string) (storage.Iter[types.IssueWithDependencyMetadata], error) {
	return nil, r.rec("IterDependentsWithMetadata")
}
func (r *recordingStorage) IterDependenciesWithMetadata(context.Context, string) (storage.Iter[types.IssueWithDependencyMetadata], error) {
	return nil, r.rec("IterDependenciesWithMetadata")
}
func (r *recordingStorage) IterIssueComments(context.Context, string) (storage.Iter[types.Comment], error) {
	return nil, r.rec("IterIssueComments")
}
func (r *recordingStorage) IterEvents(context.Context, string, int) (storage.Iter[types.Event], error) {
	return nil, r.rec("IterEvents")
}
func (r *recordingStorage) IterAllEventsSince(context.Context, time.Time) (storage.Iter[types.Event], error) {
	return nil, r.rec("IterAllEventsSince")
}
func (r *recordingStorage) IterReadyWork(context.Context, types.WorkFilter) (storage.Iter[types.Issue], error) {
	return nil, r.rec("IterReadyWork")
}
func (r *recordingStorage) IterBlockedIssues(context.Context, types.WorkFilter) (storage.Iter[types.BlockedIssue], error) {
	return nil, r.rec("IterBlockedIssues")
}
func (r *recordingStorage) IterWisps(context.Context, types.WispFilter) (storage.Iter[types.Issue], error) {
	return nil, r.rec("IterWisps")
}

// ── Statistics ──
func (r *recordingStorage) GetStatistics(context.Context) (*types.Statistics, error) {
	// Return non-nil stats on success so the gauge-recording branch runs.
	if r.err != nil {
		return nil, r.rec("GetStatistics")
	}
	_ = r.rec("GetStatistics")
	return &types.Statistics{}, nil
}

// ── Configuration ──
func (r *recordingStorage) SetConfig(context.Context, string, string) error {
	return r.rec("SetConfig")
}
func (r *recordingStorage) GetConfig(context.Context, string) (string, error) {
	return "", r.rec("GetConfig")
}
func (r *recordingStorage) GetAllConfig(context.Context) (map[string]string, error) {
	return nil, r.rec("GetAllConfig")
}
func (r *recordingStorage) SetLocalMetadata(context.Context, string, string) error {
	return r.rec("SetLocalMetadata")
}
func (r *recordingStorage) GetLocalMetadata(context.Context, string) (string, error) {
	return "", r.rec("GetLocalMetadata")
}

// ── Transactions ──
func (r *recordingStorage) RunInTransaction(context.Context, string, func(tx storage.Transaction) error) error {
	return r.rec("RunInTransaction")
}

// ── MergeSlot ──
func (r *recordingStorage) MergeSlotCreate(context.Context, string) (*types.Issue, error) {
	return nil, r.rec("MergeSlotCreate")
}
func (r *recordingStorage) MergeSlotCheck(context.Context) (*storage.MergeSlotStatus, error) {
	return nil, r.rec("MergeSlotCheck")
}
func (r *recordingStorage) MergeSlotAcquire(context.Context, string, string, bool) (*storage.MergeSlotResult, error) {
	return nil, r.rec("MergeSlotAcquire")
}
func (r *recordingStorage) MergeSlotRelease(context.Context, string, string) error {
	return r.rec("MergeSlotRelease")
}

// ── Metadata slots ──
func (r *recordingStorage) SlotSet(context.Context, string, string, string, string) error {
	return r.rec("SlotSet")
}
func (r *recordingStorage) SlotGet(context.Context, string, string) (string, error) {
	return "", r.rec("SlotGet")
}
func (r *recordingStorage) SlotClear(context.Context, string, string, string) error {
	return r.rec("SlotClear")
}

// ── Lifecycle ──
func (r *recordingStorage) Close() error {
	return r.rec("Close")
}

var _ storage.Storage = (*recordingStorage)(nil)

// enableTelemetry turns on stdout telemetry for a test and installs a cleanup
// that shuts it down. Shared by the delegation-matrix subtests.
func enableTelemetry(t *testing.T) {
	t.Helper()
	t.Setenv("BD_OTEL_METRICS_URL", "")
	t.Setenv("BD_OTEL_STDOUT", "true")
	shutdownFns = nil
	if err := Init(context.Background(), "beads-test", "v0"); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() { Shutdown(context.Background()) })
}

// TestInstrumentedStorageDelegationMatrix exercises every InstrumentedStorage
// wrapper's success and error path. For each wrapper it asserts:
//   - the call is delegated to the inner store (recorded name matches), and
//   - the inner error is propagated unchanged on the error path (and nil on
//     the success path).
//
// Iterator wrappers return (nil iter, err); the invoke closure only inspects
// the error, which is the delegation signal the wrapper controls.
func TestInstrumentedStorageDelegationMatrix(t *testing.T) {
	enableTelemetry(t)

	ctx := context.Background()

	// Each entry names the wrapper and invokes it, returning the error the
	// wrapper produced. The expected recorded call name equals the entry name.
	cases := []struct {
		name   string
		invoke func(s storage.Storage) error
	}{
		{"CreateIssue", func(s storage.Storage) error { return s.CreateIssue(ctx, &types.Issue{}, "a") }},
		{"CreateIssues", func(s storage.Storage) error { return s.CreateIssues(ctx, nil, "a") }},
		{"GetIssue", func(s storage.Storage) error { _, e := s.GetIssue(ctx, "x"); return e }},
		{"GetIssueByExternalRef", func(s storage.Storage) error { _, e := s.GetIssueByExternalRef(ctx, "x"); return e }},
		{"GetIssuesByIDs", func(s storage.Storage) error { _, e := s.GetIssuesByIDs(ctx, []string{"x"}); return e }},
		{"UpdateIssue", func(s storage.Storage) error { return s.UpdateIssue(ctx, "x", map[string]interface{}{"k": 1}, "a") }},
		{"ReopenIssue", func(s storage.Storage) error { return s.ReopenIssue(ctx, "x", "r", "a") }},
		{"UpdateIssueType", func(s storage.Storage) error { return s.UpdateIssueType(ctx, "x", "task", "a") }},
		{"CloseIssue", func(s storage.Storage) error { return s.CloseIssue(ctx, "x", "r", "a", "s") }},
		{"DeleteIssue", func(s storage.Storage) error { return s.DeleteIssue(ctx, "x") }},
		{"SearchIssues", func(s storage.Storage) error { _, e := s.SearchIssues(ctx, "q", types.IssueFilter{}); return e }},
		{"SearchIssuesWithCounts", func(s storage.Storage) error {
			_, e := s.SearchIssuesWithCounts(ctx, "q", types.IssueFilter{})
			return e
		}},

		{"AddDependency", func(s storage.Storage) error { return s.AddDependency(ctx, &types.Dependency{}, "a") }},
		{"RemoveDependency", func(s storage.Storage) error { return s.RemoveDependency(ctx, "x", "y", "a") }},
		{"GetDependencies", func(s storage.Storage) error { _, e := s.GetDependencies(ctx, "x"); return e }},
		{"GetDependents", func(s storage.Storage) error { _, e := s.GetDependents(ctx, "x"); return e }},
		{"GetDependenciesWithMetadata", func(s storage.Storage) error { _, e := s.GetDependenciesWithMetadata(ctx, "x"); return e }},
		{"GetDependentsWithMetadata", func(s storage.Storage) error { _, e := s.GetDependentsWithMetadata(ctx, "x"); return e }},
		{"GetDependencyTree", func(s storage.Storage) error { _, e := s.GetDependencyTree(ctx, "x", 3, false, false); return e }},

		{"AddLabel", func(s storage.Storage) error { return s.AddLabel(ctx, "x", "l", "a") }},
		{"RemoveLabel", func(s storage.Storage) error { return s.RemoveLabel(ctx, "x", "l", "a") }},
		{"GetLabels", func(s storage.Storage) error { _, e := s.GetLabels(ctx, "x"); return e }},
		{"GetIssuesByLabel", func(s storage.Storage) error { _, e := s.GetIssuesByLabel(ctx, "l"); return e }},

		{"GetReadyWork", func(s storage.Storage) error { _, e := s.GetReadyWork(ctx, types.WorkFilter{}); return e }},
		{"GetReadyWorkWithCounts", func(s storage.Storage) error { _, e := s.GetReadyWorkWithCounts(ctx, types.WorkFilter{}); return e }},
		{"GetBlockedIssues", func(s storage.Storage) error { _, e := s.GetBlockedIssues(ctx, types.WorkFilter{}); return e }},
		{"GetEpicsEligibleForClosure", func(s storage.Storage) error { _, e := s.GetEpicsEligibleForClosure(ctx); return e }},

		{"ListWisps", func(s storage.Storage) error { _, e := s.ListWisps(ctx, types.WispFilter{}); return e }},

		{"AddIssueComment", func(s storage.Storage) error { _, e := s.AddIssueComment(ctx, "x", "a", "t"); return e }},
		{"GetIssueComments", func(s storage.Storage) error { _, e := s.GetIssueComments(ctx, "x"); return e }},
		{"GetEvents", func(s storage.Storage) error { _, e := s.GetEvents(ctx, "x", 10); return e }},
		{"GetAllEventsSince", func(s storage.Storage) error { _, e := s.GetAllEventsSince(ctx, time.Unix(0, 0)); return e }},

		{"CountIssues", func(s storage.Storage) error { _, e := s.CountIssues(ctx, "q", types.IssueFilter{}); return e }},
		{"CountIssuesByGroup", func(s storage.Storage) error {
			_, e := s.CountIssuesByGroup(ctx, types.IssueFilter{}, "status")
			return e
		}},
		{"CountDependents", func(s storage.Storage) error { _, e := s.CountDependents(ctx, "x"); return e }},
		{"CountDependencies", func(s storage.Storage) error { _, e := s.CountDependencies(ctx, "x"); return e }},
		{"CountIssueComments", func(s storage.Storage) error { _, e := s.CountIssueComments(ctx, "x"); return e }},
		{"CountEvents", func(s storage.Storage) error { _, e := s.CountEvents(ctx, "x", 5); return e }},

		{"IterIssues", func(s storage.Storage) error { _, e := s.IterIssues(ctx, "q", types.IssueFilter{}); return e }},
		{"IterDependentsWithMetadata", func(s storage.Storage) error { _, e := s.IterDependentsWithMetadata(ctx, "x"); return e }},
		{"IterDependenciesWithMetadata", func(s storage.Storage) error { _, e := s.IterDependenciesWithMetadata(ctx, "x"); return e }},
		{"IterIssueComments", func(s storage.Storage) error { _, e := s.IterIssueComments(ctx, "x"); return e }},
		{"IterEvents", func(s storage.Storage) error { _, e := s.IterEvents(ctx, "x", 10); return e }},
		{"IterAllEventsSince", func(s storage.Storage) error { _, e := s.IterAllEventsSince(ctx, time.Unix(0, 0)); return e }},
		{"IterReadyWork", func(s storage.Storage) error { _, e := s.IterReadyWork(ctx, types.WorkFilter{}); return e }},
		{"IterBlockedIssues", func(s storage.Storage) error { _, e := s.IterBlockedIssues(ctx, types.WorkFilter{}); return e }},
		{"IterWisps", func(s storage.Storage) error { _, e := s.IterWisps(ctx, types.WispFilter{}); return e }},

		{"GetStatistics", func(s storage.Storage) error { _, e := s.GetStatistics(ctx); return e }},

		{"SetConfig", func(s storage.Storage) error { return s.SetConfig(ctx, "k", "v") }},
		{"GetConfig", func(s storage.Storage) error { _, e := s.GetConfig(ctx, "k"); return e }},
		{"GetAllConfig", func(s storage.Storage) error { _, e := s.GetAllConfig(ctx); return e }},
		{"SetLocalMetadata", func(s storage.Storage) error { return s.SetLocalMetadata(ctx, "k", "v") }},
		{"GetLocalMetadata", func(s storage.Storage) error { _, e := s.GetLocalMetadata(ctx, "k"); return e }},

		{"RunInTransaction", func(s storage.Storage) error {
			return s.RunInTransaction(ctx, "msg", func(storage.Transaction) error { return nil })
		}},

		{"MergeSlotCreate", func(s storage.Storage) error { _, e := s.MergeSlotCreate(ctx, "a"); return e }},
		{"MergeSlotCheck", func(s storage.Storage) error { _, e := s.MergeSlotCheck(ctx); return e }},
		{"MergeSlotAcquire", func(s storage.Storage) error { _, e := s.MergeSlotAcquire(ctx, "h", "a", false); return e }},
		{"MergeSlotRelease", func(s storage.Storage) error { return s.MergeSlotRelease(ctx, "h", "a") }},

		{"SlotSet", func(s storage.Storage) error { return s.SlotSet(ctx, "x", "k", "v", "a") }},
		{"SlotGet", func(s storage.Storage) error { _, e := s.SlotGet(ctx, "x", "k"); return e }},
		{"SlotClear", func(s storage.Storage) error { return s.SlotClear(ctx, "x", "k", "a") }},

		{"Close", func(s storage.Storage) error { return s.Close() }},
	}

	sentinel := errors.New("delegation-boom")

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Success path: no error, delegation recorded.
			inner := &recordingStorage{}
			wrapped := WrapStorage(inner)
			if _, ok := wrapped.(*InstrumentedStorage); !ok {
				t.Fatalf("expected *InstrumentedStorage, got %T", wrapped)
			}
			if err := tc.invoke(wrapped); err != nil {
				t.Errorf("success path returned err = %v, want nil", err)
			}
			if inner.lastCall != tc.name {
				t.Errorf("delegated to %q, want %q", inner.lastCall, tc.name)
			}

			// Error path: inner returns sentinel, wrapper propagates it.
			innerErr := &recordingStorage{err: sentinel}
			wrappedErr := WrapStorage(innerErr)
			if err := tc.invoke(wrappedErr); !errors.Is(err, sentinel) {
				t.Errorf("error path returned err = %v, want sentinel", err)
			}
			if innerErr.lastCall != tc.name {
				t.Errorf("error path delegated to %q, want %q", innerErr.lastCall, tc.name)
			}
		})
	}
}
