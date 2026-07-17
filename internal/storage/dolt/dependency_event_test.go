//go:build cgo

package dolt

import (
	"context"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/types"
)

// TestDependencyMutationsWriteAuditEvents verifies beads-1qt9: bd dep
// add/remove must write EventDependencyAdded/EventDependencyRemoved audit
// events so dependency-graph mutations are visible in `bd history`. Before the
// fix, Add/RemoveDependencyInTx changed the edge but wrote no event, leaving
// graph changes un-auditable.
func TestDependencyMutationsWriteAuditEvents(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx, cancel := testContext(t)
	defer cancel()

	// Wide window: events default created_at to CURRENT_TIMESTAMP at
	// second granularity, and the events-since query is strict (created_at >
	// since), so a sub-second window can exclude same-second events.
	since := time.Now().UTC().Add(-5 * time.Second)

	a := &types.Issue{ID: "dep-ev-a", Title: "A", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask}
	b := &types.Issue{ID: "dep-ev-b", Title: "B", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask}
	if err := store.CreateIssue(ctx, a, "tester"); err != nil {
		t.Fatalf("create a: %v", err)
	}
	if err := store.CreateIssue(ctx, b, "tester"); err != nil {
		t.Fatalf("create b: %v", err)
	}

	dep := &types.Dependency{IssueID: a.ID, DependsOnID: b.ID, Type: types.DepBlocks}
	if err := store.AddDependency(ctx, dep, "tester"); err != nil {
		t.Fatalf("AddDependency: %v", err)
	}

	if !hasEvent(t, store, ctx, since, a.ID, types.EventDependencyAdded) {
		t.Errorf("expected an EventDependencyAdded event for %s after dep add", a.ID)
	}

	if err := store.RemoveDependency(ctx, a.ID, b.ID, "tester"); err != nil {
		t.Fatalf("RemoveDependency: %v", err)
	}

	if !hasEvent(t, store, ctx, since, a.ID, types.EventDependencyRemoved) {
		t.Errorf("expected an EventDependencyRemoved event for %s after dep remove", a.ID)
	}
}

func hasEvent(t *testing.T, store *DoltStore, ctx context.Context, since time.Time, issueID string, et types.EventType) bool {
	t.Helper()
	events, err := store.GetAllEventsSince(ctx, since)
	if err != nil {
		t.Fatalf("GetAllEventsSince: %v", err)
	}
	for _, e := range events {
		if e.IssueID == issueID && e.EventType == et {
			return true
		}
	}
	return false
}

// eventActor returns the actor recorded on the first matching event, or "" if
// none is found.
func eventActor(t *testing.T, store *DoltStore, ctx context.Context, since time.Time, issueID string, et types.EventType) string {
	t.Helper()
	events, err := store.GetAllEventsSince(ctx, since)
	if err != nil {
		t.Fatalf("GetAllEventsSince: %v", err)
	}
	for _, e := range events {
		if e.IssueID == issueID && e.EventType == et {
			return e.Actor
		}
	}
	return ""
}

// TestDependencyRemoveEventCreditsActor verifies beads-gmiu: the
// EventDependencyRemoved audit event must be credited to the caller's actor,
// mirroring EventDependencyAdded. Before the fix RemoveDependencyInTx hardcoded
// "system", so `bd history` mis-attributed every dependency removal regardless
// of who ran `bd dep remove`.
func TestDependencyRemoveEventCreditsActor(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx, cancel := testContext(t)
	defer cancel()

	since := time.Now().UTC().Add(-5 * time.Second)

	a := &types.Issue{ID: "dep-actor-a", Title: "A", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask}
	b := &types.Issue{ID: "dep-actor-b", Title: "B", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask}
	if err := store.CreateIssue(ctx, a, "creator"); err != nil {
		t.Fatalf("create a: %v", err)
	}
	if err := store.CreateIssue(ctx, b, "creator"); err != nil {
		t.Fatalf("create b: %v", err)
	}

	dep := &types.Dependency{IssueID: a.ID, DependsOnID: b.ID, Type: types.DepBlocks}
	if err := store.AddDependency(ctx, dep, "adder"); err != nil {
		t.Fatalf("AddDependency: %v", err)
	}

	const remover = "alice"
	if err := store.RemoveDependency(ctx, a.ID, b.ID, remover); err != nil {
		t.Fatalf("RemoveDependency: %v", err)
	}

	// The add event should already credit the real actor (baseline, beads-1qt9).
	if got := eventActor(t, store, ctx, since, a.ID, types.EventDependencyAdded); got != "adder" {
		t.Errorf("EventDependencyAdded actor = %q, want %q", got, "adder")
	}

	// beads-gmiu: the remove event must credit the caller, not "system".
	got := eventActor(t, store, ctx, since, a.ID, types.EventDependencyRemoved)
	if got != remover {
		t.Errorf("EventDependencyRemoved actor = %q, want %q (was hardcoded \"system\" before beads-gmiu)", got, remover)
	}
}
