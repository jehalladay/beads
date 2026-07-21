//go:build cgo

package main

import (
	"context"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestProxiedCreateNoopEvent_64nbj is the teeth for beads-64nbj: the PROXIED
// (domain/db) issue Insert must NOT record a second `created` history event
// when `bd create --force --id <existing>` OVERWRITES an already-present row.
// insertIssueRow runs INSERT ... ON DUPLICATE KEY UPDATE, so an --id reuse is
// an UPDATE, not a create — but the leg recorded types.EventCreated
// unconditionally, so a hub-connected / server-mode client wrote a phantom
// "created" event on an overwrite. The direct path guards this (only records
// when isNew: InsertIssueIfNew existingCount==0). This is the create-event twin
// of beads-5vpoh (the proxied label no-op-event fix).
//
// The proxied harness is mandatory: the bug lives in the domain/db repo
// (issueSQLRepositoryImpl.Insert), reached in proxied-server mode. The RED
// entrypoint is `bd create --force --id <existing>` (the beads-k75k overwrite
// path); without --force the CLI refuses the reuse before reaching the repo.
func TestProxiedCreateNoopEvent_64nbj(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	createdEventCount := func(t *testing.T, p proxiedProject, id string) int {
		t.Helper()
		db := openProxiedDB(t, p)
		var count int
		if err := db.QueryRowContext(context.Background(),
			"SELECT COUNT(*) FROM events WHERE issue_id = ? AND event_type = ?",
			id, string(types.EventCreated)).Scan(&count); err != nil {
			t.Fatalf("count created events for %s: %v", id, err)
		}
		return count
	}

	t.Run("force_id_overwrite_records_no_second_created_event", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "cnb")
		a := bdProxiedCreate(t, bd, p.dir, "Original", "--type", "task")

		// The fresh create recorded exactly one `created` event (regression
		// guard: the fix must NOT under-record a genuine first create).
		if got := createdEventCount(t, p, a.ID); got != 1 {
			t.Fatalf("after fresh create: created count = %d, want 1", got)
		}

		// Re-create with the SAME id via --force: insertIssueRow's ON DUPLICATE
		// KEY UPDATE overwrites the existing row (an UPDATE, not a create) → it
		// must NOT record a second `created` event (RED before the fix: count 2).
		if _, err := bdProxiedRun(t, bd, p.dir, "create", "Overwrite", "--type", "task", "--force", "--id", a.ID); err != nil {
			t.Fatalf("force overwrite create failed: %v", err)
		}
		if got := createdEventCount(t, p, a.ID); got != 1 {
			t.Errorf("after --force --id overwrite: created count = %d, want 1 (no phantom created event on an ODKU overwrite)", got)
		}
	})
}
