package telemetry

import (
	"context"
	"errors"
	"testing"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
)

// fakeStorage is a partial storage.Storage: it embeds the interface so it
// satisfies all 64 methods, but only the ones exercised by these tests are
// overridden. Any un-overridden method would panic (nil interface) if called —
// which is the desired safety net (a test that reaches an unstubbed method
// fails loudly rather than silently passing).
type fakeStorage struct {
	storage.Storage

	getIssueCalls  int
	deleteErr      error
	lastDeletedID  string
	stats          *types.Statistics
	statsErr       error
	getStatsCalled int
}

func (f *fakeStorage) GetIssue(_ context.Context, id string) (*types.Issue, error) {
	f.getIssueCalls++
	return &types.Issue{ID: id}, nil
}

func (f *fakeStorage) DeleteIssue(_ context.Context, id string) error {
	f.lastDeletedID = id
	return f.deleteErr
}

func (f *fakeStorage) GetStatistics(_ context.Context) (*types.Statistics, error) {
	f.getStatsCalled++
	return f.stats, f.statsErr
}

// TestWrapStorageDisabledReturnsInner: with telemetry off, WrapStorage must
// return the exact same inner storage (no wrapping, zero overhead).
func TestWrapStorageDisabledReturnsInner(t *testing.T) {
	t.Setenv("BD_OTEL_METRICS_URL", "")
	t.Setenv("BD_OTEL_STDOUT", "")

	inner := &fakeStorage{}
	got := WrapStorage(inner)
	if got != storage.Storage(inner) {
		t.Fatalf("WrapStorage(inner) = %T (%p), want the same inner (%p) when disabled", got, got, inner)
	}
}

// TestWrapStorageEnabledWraps: with telemetry on, WrapStorage must return a
// distinct *InstrumentedStorage that delegates to inner.
func TestWrapStorageEnabledWraps(t *testing.T) {
	t.Setenv("BD_OTEL_METRICS_URL", "")
	t.Setenv("BD_OTEL_STDOUT", "true")
	shutdownFns = nil
	if err := Init(context.Background(), "beads-test", "v0"); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() { Shutdown(context.Background()) })

	inner := &fakeStorage{}
	got := WrapStorage(inner)
	wrapped, ok := got.(*InstrumentedStorage)
	if !ok {
		t.Fatalf("WrapStorage(inner) = %T, want *InstrumentedStorage when enabled", got)
	}
	if wrapped.inner != storage.Storage(inner) {
		t.Error("wrapped.inner is not the provided inner storage")
	}
}

// TestInstrumentedDelegatesSuccess: a wrapped read delegates to inner and
// returns its result, with instrumentation transparent to the caller.
func TestInstrumentedDelegatesSuccess(t *testing.T) {
	t.Setenv("BD_OTEL_METRICS_URL", "")
	t.Setenv("BD_OTEL_STDOUT", "true")
	shutdownFns = nil
	if err := Init(context.Background(), "beads-test", "v0"); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() { Shutdown(context.Background()) })

	inner := &fakeStorage{}
	wrapped := WrapStorage(inner)

	issue, err := wrapped.GetIssue(context.Background(), "bd-42")
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if issue == nil || issue.ID != "bd-42" {
		t.Fatalf("GetIssue returned %+v, want issue with ID bd-42", issue)
	}
	if inner.getIssueCalls != 1 {
		t.Errorf("inner.GetIssue called %d times, want 1 (delegation)", inner.getIssueCalls)
	}
}

// TestInstrumentedDelegatesError: the wrapper records the error (done() path)
// and propagates it unchanged.
func TestInstrumentedDelegatesError(t *testing.T) {
	t.Setenv("BD_OTEL_METRICS_URL", "")
	t.Setenv("BD_OTEL_STDOUT", "true")
	shutdownFns = nil
	if err := Init(context.Background(), "beads-test", "v0"); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() { Shutdown(context.Background()) })

	sentinel := errors.New("boom")
	inner := &fakeStorage{deleteErr: sentinel}
	wrapped := WrapStorage(inner)

	err := wrapped.DeleteIssue(context.Background(), "bd-7")
	if !errors.Is(err, sentinel) {
		t.Fatalf("DeleteIssue err = %v, want sentinel propagated", err)
	}
	if inner.lastDeletedID != "bd-7" {
		t.Errorf("inner.DeleteIssue got id %q, want bd-7", inner.lastDeletedID)
	}
}

// TestInstrumentedGetStatisticsGaugePath: GetStatistics has a special branch
// that records issue-count gauges when the result is non-nil and error-free.
// Exercise both the happy (gauge-recording) path and the error path.
func TestInstrumentedGetStatisticsGaugePath(t *testing.T) {
	t.Setenv("BD_OTEL_METRICS_URL", "")
	t.Setenv("BD_OTEL_STDOUT", "true")
	shutdownFns = nil
	if err := Init(context.Background(), "beads-test", "v0"); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() { Shutdown(context.Background()) })

	// Happy path: non-nil stats → gauge branch runs without panicking.
	inner := &fakeStorage{stats: &types.Statistics{
		OpenIssues:       3,
		InProgressIssues: 1,
		ClosedIssues:     10,
		DeferredIssues:   2,
	}}
	wrapped := WrapStorage(inner)
	got, err := wrapped.GetStatistics(context.Background())
	if err != nil {
		t.Fatalf("GetStatistics: %v", err)
	}
	if got == nil || got.OpenIssues != 3 {
		t.Fatalf("GetStatistics = %+v, want the fake's stats", got)
	}

	// Error path: nil stats + error → gauge branch skipped, error propagated.
	sentinel := errors.New("stat failure")
	innerErr := &fakeStorage{statsErr: sentinel}
	wrappedErr := WrapStorage(innerErr)
	if _, err := wrappedErr.GetStatistics(context.Background()); !errors.Is(err, sentinel) {
		t.Fatalf("GetStatistics err = %v, want sentinel", err)
	}
}
