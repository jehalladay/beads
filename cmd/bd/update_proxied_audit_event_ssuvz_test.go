//go:build cgo

package main

import (
	"context"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestProxiedUpdateAuditEvent_ssuvz is the teeth for beads-ssuvz: the PROXIED
// (domain/db) issueSQLRepositoryImpl.Update must record the SAME specific audit
// event TYPE the direct twin (issueops.updateIssueInTx → DetermineEventType)
// records for a status transition — closed / reopened / status_changed — with
// OldValue=json(oldIssue) / NewValue=json(updates), NOT a flattened generic
// "updated" with empty values. Before the fix the proxied Update hardcoded
// types.EventUpdated + empty old/new on every update, so `bd update --status
// closed` over a hub-connected / server-mode client left a lower-fidelity audit
// trail than the direct path (a direct/proxied audit-emission parity gap, twin
// of beads-c5efw / beads-64nbj).
//
// Ground-truth is the events table itself (via openProxiedDB), not `bd history`
// wording. A non-status update control proves EventUpdated is still emitted
// where it should be.
func TestProxiedUpdateAuditEvent_ssuvz(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	// countEvent returns how many events of the given type exist for id.
	countEvent := func(t *testing.T, p proxiedProject, id string, evt types.EventType) int {
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

	// latestEvent returns the (event_type, old_value, new_value) of the most
	// recent event for id, to assert the value diff is populated.
	latestEvent := func(t *testing.T, p proxiedProject, id string) (string, string, string) {
		t.Helper()
		db := openProxiedDB(t, p)
		var et, ov, nv string
		if err := db.QueryRowContext(context.Background(),
			"SELECT event_type, old_value, new_value FROM events WHERE issue_id = ? ORDER BY created_at DESC, id DESC LIMIT 1",
			id).Scan(&et, &ov, &nv); err != nil {
			t.Fatalf("latest event for %s: %v", id, err)
		}
		return et, ov, nv
	}

	t.Run("update_status_closed_records_EventClosed_with_values", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "upev1")
		a := bdProxiedCreate(t, bd, p.dir, "Closeme", "--type", "task")

		if _, err := bdProxiedRun(t, bd, p.dir, "update", a.ID, "--status", "closed"); err != nil {
			t.Fatalf("update --status closed failed: %v", err)
		}

		// RED before the fix: 0 (proxied recorded EventUpdated). Direct path writes 1.
		if got := countEvent(t, p, a.ID, types.EventClosed); got != 1 {
			t.Errorf("update --status closed: EventClosed count = %d, want 1 (proxied must record the specific event type like the direct path)", got)
		}
		et, ov, nv := latestEvent(t, p, a.ID)
		if et != string(types.EventClosed) {
			t.Errorf("latest event type = %q, want %q", et, types.EventClosed)
		}
		// OldValue = json(oldIssue) (non-empty object), NewValue = json(updates)
		// (non-empty object). Before the fix both were empty.
		if len(ov) < 2 || ov == "null" {
			t.Errorf("EventClosed OldValue = %q, want a populated json(oldIssue)", ov)
		}
		if len(nv) < 2 || nv == "null" {
			t.Errorf("EventClosed NewValue = %q, want a populated json(updates)", nv)
		}
	})

	t.Run("update_status_open_on_closed_records_EventReopened", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "upev2")
		a := bdProxiedCreate(t, bd, p.dir, "Reopenme", "--type", "task")

		if _, err := bdProxiedRun(t, bd, p.dir, "update", a.ID, "--status", "closed"); err != nil {
			t.Fatalf("update --status closed failed: %v", err)
		}
		if _, err := bdProxiedRun(t, bd, p.dir, "update", a.ID, "--status", "open"); err != nil {
			t.Fatalf("update --status open failed: %v", err)
		}

		if got := countEvent(t, p, a.ID, types.EventReopened); got != 1 {
			t.Errorf("update closed→open: EventReopened count = %d, want 1", got)
		}
	})

	t.Run("update_status_in_progress_records_EventStatusChanged", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "upev3")
		a := bdProxiedCreate(t, bd, p.dir, "Startme", "--type", "task")

		if _, err := bdProxiedRun(t, bd, p.dir, "update", a.ID, "--status", "in_progress"); err != nil {
			t.Fatalf("update --status in_progress failed: %v", err)
		}

		if got := countEvent(t, p, a.ID, types.EventStatusChanged); got != 1 {
			t.Errorf("update open→in_progress: EventStatusChanged count = %d, want 1", got)
		}
		// And it must NOT have recorded a generic EventUpdated for the status change.
		if got := countEvent(t, p, a.ID, types.EventUpdated); got != 0 {
			t.Errorf("update open→in_progress: EventUpdated count = %d, want 0 (status change must not flatten to 'updated')", got)
		}
	})

	t.Run("non_status_update_records_EventUpdated", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "upev4")
		a := bdProxiedCreate(t, bd, p.dir, "Assignme", "--type", "task")

		if _, err := bdProxiedRun(t, bd, p.dir, "update", a.ID, "--assignee", "alice"); err != nil {
			t.Fatalf("update --assignee failed: %v", err)
		}

		// A non-status update stays EventUpdated (parity with DetermineEventType,
		// which returns EventUpdated when no status key is present).
		if got := countEvent(t, p, a.ID, types.EventUpdated); got != 1 {
			t.Errorf("non-status update: EventUpdated count = %d, want 1", got)
		}
		if got := countEvent(t, p, a.ID, types.EventStatusChanged); got != 0 {
			t.Errorf("non-status update: EventStatusChanged count = %d, want 0", got)
		}
	})
}
