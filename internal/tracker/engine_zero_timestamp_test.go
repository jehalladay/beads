//go:build cgo

package tracker

import (
	"context"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/types"
)

// TestEnginePushDoesNotClobberExternalWithZeroTimestamp is the beads-mckz
// Hazard-1 regression: when the external issue's UpdatedAt is the zero value
// (the provider response omitted/failed to parse updated_at — github/gitlab
// type it as *time.Time and set it only when non-nil; jira/linear leave it
// zero on a parse error), the default push-skip comparison
// `!extIssue.UpdatedAt.Before(issue.UpdatedAt)` evaluates false because the
// zero time is "before" any real local time. Without a guard the engine then
// treats the external as infinitely old and PUSHES local over it — silently
// clobbering the authoritative external side. A zero external timestamp means
// "age unknown", so the safe behavior is to skip (never overwrite blindly).
func TestEnginePushDoesNotClobberExternalWithZeroTimestamp(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	defer store.Close()

	extRef := "https://test.test/EXT-1"
	local := &types.Issue{
		ID:          "bd-zerots",
		Title:       "Local title",
		Status:      types.StatusOpen,
		IssueType:   types.TypeTask,
		Priority:    2,
		ExternalRef: strPtr(extRef),
		UpdatedAt:   time.Now().UTC(), // local looks recent
	}
	if err := store.CreateIssue(ctx, local, "test-actor"); err != nil {
		t.Fatalf("CreateIssue() error: %v", err)
	}

	tracker := newMockTracker("test")
	tracker.issues = []TrackerIssue{{
		ID:         "EXT-1",
		Identifier: "EXT-1",
		URL:        extRef,
		Title:      "External title (must NOT be clobbered)",
		// UpdatedAt deliberately left as the zero value: the provider gave no
		// usable timestamp.
	}}

	engine := NewEngine(tracker, store, "test-actor")
	result, err := engine.Sync(ctx, SyncOptions{Push: true})
	if err != nil {
		t.Fatalf("Sync() error: %v", err)
	}
	if !result.Success {
		t.Fatalf("Sync() not successful: %s", result.Error)
	}

	if len(tracker.updated) != 0 {
		t.Fatalf("push clobbered an external issue of UNKNOWN age (zero UpdatedAt): updated=%v; want it skipped, not overwritten", tracker.updated)
	}
	if result.PushStats.Updated != 0 {
		t.Errorf("PushStats.Updated = %d, want 0 (zero-timestamp external must not be counted as pushed)", result.PushStats.Updated)
	}
}
