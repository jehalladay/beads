//go:build cgo

package main

import (
	"context"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestProxiedLabelNoopEvent_5vpoh is the teeth for beads-5vpoh: the PROXIED
// (domain/db) label Insert/Delete must NOT record a label_added / label_removed
// history event when the underlying INSERT IGNORE / DELETE affected ZERO rows
// (a duplicate add, or removing an absent label). The direct path already
// guarded this (issueops.AddLabelInTx / RemoveLabelInTx, beads-usz1) by checking
// RowsAffected==0 before recording; this leg landed unguarded, so a hub-connected
// / server-mode client wrote spurious no-op audit-trail events.
//
// The bug only manifests through the domain/db repo (labelSQLRepositoryImpl.
// Insert/Delete), which is exercised in proxied-server mode — hence the proxied
// harness is mandatory; a pure direct test would not cover it. The RED entrypoint
// is `bd update --add-label/--remove-label`, whose proxied path applies labels
// unconditionally via the AddLabels/RemoveLabels usecase (repo.Insert/Delete per
// label with NO no-op pre-filter) — unlike the `bd label add` verb, which filters
// no-ops at the CLI layer (beads-4zy65) and so would not reach the buggy repo.
func TestProxiedLabelNoopEvent_5vpoh(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	labelEventCount := func(t *testing.T, p proxiedProject, id string, evt types.EventType) int {
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

	t.Run("duplicate_add_records_no_second_event", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "vpadd")
		a := bdProxiedCreate(t, bd, p.dir, "Dup add", "--type", "task")

		// First add of a genuinely-new label → exactly one label_added event
		// (regression guard: the fix must NOT under-record real inserts).
		if _, err := bdProxiedRun(t, bd, p.dir, "update", a.ID, "--add-label", "keep"); err != nil {
			t.Fatalf("first add failed: %v", err)
		}
		if got := labelEventCount(t, p, a.ID, types.EventLabelAdded); got != 1 {
			t.Fatalf("after first add: label_added count = %d, want 1", got)
		}

		// Re-adding the SAME label is an INSERT IGNORE no-op (0 rows) → it must
		// NOT record a second label_added event (RED before the fix: count == 2).
		if _, err := bdProxiedRun(t, bd, p.dir, "update", a.ID, "--add-label", "keep"); err != nil {
			t.Fatalf("duplicate add failed: %v", err)
		}
		if got := labelEventCount(t, p, a.ID, types.EventLabelAdded); got != 1 {
			t.Errorf("after duplicate add: label_added count = %d, want 1 (no spurious no-op event)", got)
		}
	})

	t.Run("remove_absent_records_no_event", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "vprm")
		a := bdProxiedCreate(t, bd, p.dir, "Absent remove", "--type", "task")

		// Removing a label the issue never carried is a DELETE no-op (0 rows) →
		// it must NOT record a label_removed event (RED before the fix: count == 1).
		if _, err := bdProxiedRun(t, bd, p.dir, "update", a.ID, "--remove-label", "ghost"); err != nil {
			t.Fatalf("remove absent failed: %v", err)
		}
		if got := labelEventCount(t, p, a.ID, types.EventLabelRemoved); got != 0 {
			t.Errorf("after removing an absent label: label_removed count = %d, want 0 (no spurious no-op event)", got)
		}

		// Removing a PRESENT label DOES record exactly one label_removed event
		// (regression guard against under-recording real deletes).
		if _, err := bdProxiedRun(t, bd, p.dir, "update", a.ID, "--add-label", "real"); err != nil {
			t.Fatalf("setup add failed: %v", err)
		}
		if _, err := bdProxiedRun(t, bd, p.dir, "update", a.ID, "--remove-label", "real"); err != nil {
			t.Fatalf("remove present failed: %v", err)
		}
		if got := labelEventCount(t, p, a.ID, types.EventLabelRemoved); got != 1 {
			t.Errorf("after removing a present label: label_removed count = %d, want 1", got)
		}
	})
}
