//go:build cgo

package main

import (
	"context"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestProxiedDepAuditEvent_c5efw is the teeth for beads-c5efw: the PROXIED
// (domain/db) dependency Insert/Delete must record dependency_added /
// dependency_removed history events, mirroring the direct path
// (issueops.AddDependencyInTx / removeDependencyInTx, beads-1qt9). The proxied
// domain/db dependency repo previously recorded NO dep events at all, so a
// hub-connected / server-mode crew's `bd dep add` / `bd dep remove` left NO
// audit trail — a direct/proxied audit-parity gap (the opposite direction from
// beads-5vpoh, which was proxied recording a SPURIOUS label event).
//
// Ground-truth is the events table itself (via openProxiedDB), not `bd history`
// wording. A control (proxied `update --status`) proves the events table +
// writer work, isolating the dep-event gap.
func TestProxiedDepAuditEvent_c5efw(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	eventCount := func(t *testing.T, p proxiedProject, id string, evt types.EventType) int {
		t.Helper()
		db := openProxiedDB(t, p)
		var count int
		if err := db.QueryRowContext(context.Background(),
			"SELECT COUNT(*) FROM events WHERE issue_id = ? AND event_type = ?",
			id, string(evt)).Scan(&count); err != nil {
			t.Fatalf("count %s events for %s: %v", evt, id, err)
		}
		return count
	}

	t.Run("dep_add_records_dependency_added_event", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "depev1")
		a := bdProxiedCreate(t, bd, p.dir, "Source", "--type", "task")
		b := bdProxiedCreate(t, bd, p.dir, "Target", "--type", "task")

		// Control: the events table + history writer work over the proxied path.
		if _, err := bdProxiedRun(t, bd, p.dir, "update", a.ID, "--status", "in_progress"); err != nil {
			t.Fatalf("control update failed: %v", err)
		}

		// The dep add must record a dependency_added event on the source
		// (RED before the fix: count 0; direct path writes 1).
		bdProxiedDep(t, bd, p.dir, "add", a.ID, b.ID)
		if got := eventCount(t, p, a.ID, types.EventDependencyAdded); got != 1 {
			t.Errorf("after dep add: dependency_added count = %d, want 1 (proxied must record the audit event like the direct path)", got)
		}
	})

	t.Run("dep_remove_records_dependency_removed_event", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "depev2")
		a := bdProxiedCreate(t, bd, p.dir, "Source", "--type", "task")
		b := bdProxiedCreate(t, bd, p.dir, "Target", "--type", "task")

		bdProxiedDep(t, bd, p.dir, "add", a.ID, b.ID)
		bdProxiedDep(t, bd, p.dir, "remove", a.ID, b.ID)
		if got := eventCount(t, p, a.ID, types.EventDependencyRemoved); got != 1 {
			t.Errorf("after dep remove: dependency_removed count = %d, want 1", got)
		}
	})

	t.Run("dep_remove_absent_edge_records_no_event", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "depev3")
		a := bdProxiedCreate(t, bd, p.dir, "Source", "--type", "task")
		b := bdProxiedCreate(t, bd, p.dir, "Target", "--type", "task")

		// Removing an edge that was never added is a no-op (Found:false) — it
		// must NOT record a dependency_removed event (consistent with the direct
		// path and the 5vpoh no-op-event guard). This fails (rc!=0) at the CLI.
		_, _ = bdProxiedRun(t, bd, p.dir, "dep", "remove", a.ID, b.ID)
		if got := eventCount(t, p, a.ID, types.EventDependencyRemoved); got != 0 {
			t.Errorf("after removing an absent edge: dependency_removed count = %d, want 0 (no no-op audit event)", got)
		}
	})
}
