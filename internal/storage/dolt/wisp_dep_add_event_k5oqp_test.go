//go:build cgo

package dolt

import (
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/types"
)

// TestWispSourceDepAddWritesAuditEvent verifies beads-k5oqp: a wisp-SOURCE
// dependency add (routed through DoltStore.addWispDependency) must write an
// EventDependencyAdded audit event, mirroring the issue-source path
// (issueops.AddDependencyInTx, beads-1qt9), the embedded backend (which always
// routes through that seam), the proxied path (beads-c5efw), and the
// wisp-source REMOVE path (issueops.RemoveDependencyInTx).
//
// Before the fix, addWispDependency did its own inline INSERT into
// wisp_dependencies and recorded NO event, so on hub-connected sql-server crew
// `bd dep add <wisp> <target>` was invisible in `bd history` — a
// backend-asymmetric audit-trail hole (the sole silent dep-write leg).
func TestWispSourceDepAddWritesAuditEvent_k5oqp(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx, cancel := testContext(t)
	defer cancel()

	// Wide window: events default created_at to CURRENT_TIMESTAMP at second
	// granularity and the events-since query is strict (created_at > since).
	since := time.Now().UTC().Add(-5 * time.Second)

	const (
		wispA = "k5oqp-wisp-a"
		wispB = "k5oqp-wisp-b"
	)
	createWisp(t, ctx, store, wispA)
	createWisp(t, ctx, store, wispB)

	// Active-wisp SOURCE -> routes DoltStore.AddDependency to addWispDependency.
	dep := &types.Dependency{IssueID: wispA, DependsOnID: wispB, Type: types.DepBlocks}
	if err := store.AddDependency(ctx, dep, "adder"); err != nil {
		t.Fatalf("AddDependency (wisp source): %v", err)
	}

	if !hasEvent(t, store, ctx, since, wispA, types.EventDependencyAdded) {
		t.Errorf("expected an EventDependencyAdded event for wisp source %s after dep add (beads-k5oqp)", wispA)
	}
	if got := eventActor(t, store, ctx, since, wispA, types.EventDependencyAdded); got != "adder" {
		t.Errorf("EventDependencyAdded actor = %q, want %q", got, "adder")
	}
}
