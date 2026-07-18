//go:build cgo

package tracker

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/types"
)

// TestEnginePushSkipsWhenFetchErrorsInsteadOfClobbering is the beads-ih82
// regression: during push, the update branch fetches the external issue to run
// the newer-external staleness guard (skip if external is same-or-newer,
// beads-mckz). If that fetch fails with a NON-ratelimit error, the external's
// age is UNKNOWN — pushing anyway would silently clobber a possibly-newer
// external edit. Before the fix, a non-ratelimit fetch error fell through to
// UpdateIssue unconditionally (err != nil skipped the staleness block, and only
// ratelimit-exhausted aborted), so the external was overwritten blindly. The
// fix skips (with a warning) on any fetch error, mirroring the zero-timestamp
// handling. --force still bypasses the pre-check.
func TestEnginePushSkipsWhenFetchErrorsInsteadOfClobbering(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	defer store.Close()

	extRef := "https://test.test/EXT-1"
	local := &types.Issue{
		ID:          "bd-ferr",
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
		UpdatedAt:  time.Now().UTC(),
	}}
	// The pre-update staleness fetch fails with a transient, non-ratelimit error.
	tracker.fetchIssueErr = errors.New("transient network blip")

	engine := NewEngine(tracker, store, "test-actor")
	result, err := engine.Sync(ctx, SyncOptions{Push: true})
	if err != nil {
		t.Fatalf("Sync() error: %v", err)
	}
	if !result.Success {
		t.Fatalf("Sync() not successful: %s", result.Error)
	}

	if len(tracker.updated) != 0 {
		t.Fatalf("push clobbered the external issue despite a fetch error (age UNKNOWN): updated=%v; want it skipped, not overwritten (ih82)", tracker.updated)
	}
	if result.PushStats.Updated != 0 {
		t.Errorf("PushStats.Updated = %d, want 0 (a fetch-error issue must not be counted as pushed)", result.PushStats.Updated)
	}
	if result.PushStats.Skipped < 1 {
		t.Errorf("PushStats.Skipped = %d, want >=1 (the fetch-error issue must be skipped)", result.PushStats.Skipped)
	}
}
