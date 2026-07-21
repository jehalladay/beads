//go:build cgo

package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// TestEmbeddedUpdateNoOpDoesNotBumpUpdatedAt is the integrity teeth for
// beads-absq1. beads-bdy2 made the DISPLAY honest ("already matches (no
// change)") but left the write in place: `bd update <id> -p <same>` still ran
// UpdateIssue, which bumps updated_at (and does a trackMutation). Because
// `bd stale` orders by updated_at ASC and computes daysStale from it, a
// self-reported no-op silently RESET the staleness clock and hid a stale issue
// from `bd stale` — the integrity defect this bead fixes.
//
// The fix (cmd/bd/update.go) SKIPS the UpdateIssue write (and its trackMutation
// + audit) when the update is scalar-only AND every set scalar already equals
// the issue's current value (onlyScalarFlags && scalarUpdateIsNoOp) — so
// updated_at is NOT touched. The display branch still reports the honest no-op,
// now without a preceding write. Any genuine change, or any mixed/non-scalar
// update (notes/labels/parent/metadata/--claim), falls through to UpdateIssue
// unchanged, so this cannot swallow a real mutation.
//
// updated_at is asserted via `bd show --json` because it is written to the
// issues column regardless of Dolt auto-commit mode — the direct field the
// defect is about, and mode-independent (stronger for this bead than a Dolt
// HEAD check).
func TestEmbeddedUpdateNoOpDoesNotBumpUpdatedAt(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "ua")

	showUpdatedAt := func(t *testing.T, id string) string {
		t.Helper()
		raw := bdShowJSON(t, bd, dir, id)
		obj := parseShowJSON(t, raw)
		var fields struct {
			UpdatedAt string `json:"updated_at"`
		}
		if err := json.Unmarshal(obj, &fields); err != nil {
			t.Fatalf("parse updated_at from show JSON: %v\n%s", err, string(obj))
		}
		if fields.UpdatedAt == "" {
			t.Fatalf("no updated_at in show JSON for %s: %s", id, string(obj))
		}
		return fields.UpdatedAt
	}

	// The core integrity leg: a scalar-only no-op must NOT bump updated_at.
	t.Run("scalar_noop_priority_preserves_updated_at", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "Noop priority", "--type", "task", "--priority", "2")
		before := showUpdatedAt(t, issue.ID)

		out := bdUpdate(t, bd, dir, issue.ID, "--priority", "2")
		if strings.Contains(out, "Updated issue") {
			t.Errorf("re-setting --priority to the current value must NOT print '✓ Updated issue'; got:\n%s", out)
		}
		if !strings.Contains(strings.ToLower(out), "no change") {
			t.Errorf("expected an honest 'no change' line on a scalar-only no-op; got:\n%s", out)
		}

		after := showUpdatedAt(t, issue.ID)
		if after != before {
			t.Errorf("a scalar-only no-op update must NOT bump updated_at (would reset the bd-stale clock, beads-absq1): before=%q after=%q", before, after)
		}
	})

	// The same for --status (already-open) — the other common no-op.
	t.Run("scalar_noop_status_preserves_updated_at", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "Noop status", "--type", "task")
		before := showUpdatedAt(t, issue.ID)

		out := bdUpdate(t, bd, dir, issue.ID, "--status", "open")
		if strings.Contains(out, "Updated issue") {
			t.Errorf("re-setting --status to the current value must NOT print '✓ Updated issue'; got:\n%s", out)
		}

		after := showUpdatedAt(t, issue.ID)
		if after != before {
			t.Errorf("a scalar-only status no-op must NOT bump updated_at (beads-absq1): before=%q after=%q", before, after)
		}
	})

	// The integrity payoff: after a no-op, an issue whose updated_at was aged
	// into the stale window must STILL be listed by `bd stale` — the no-op did
	// not reset its clock.
	t.Run("noop_keeps_issue_in_bd_stale", func(t *testing.T) {
		dir2, beadsDir2, _ := bdInit(t, bd, "--prefix", "us")
		issue := bdCreate(t, bd, dir2, "Stale then noop", "--type", "task", "--priority", "2")
		makeIssuesStale(t, beadsDir2, "us", []string{issue.ID})

		// A scalar-only no-op after ageing must leave it stale.
		bdUpdate(t, bd, dir2, issue.ID, "--priority", "2")

		out := bdCommand(t, bd, dir2, "stale", "--days", "30")
		if !strings.Contains(out, issue.ID) {
			t.Errorf("a scalar-only no-op must NOT reset the staleness clock — %s should still appear in `bd stale --days 30` (beads-absq1); got:\n%s", issue.ID, out)
		}
	})

	// GUARD 1: a real scalar change MUST still bump updated_at (the skip must
	// not swallow genuine mutations).
	t.Run("real_change_still_bumps_updated_at", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "Real change", "--type", "task", "--priority", "2")
		before := showUpdatedAt(t, issue.ID)

		out := bdUpdate(t, bd, dir, issue.ID, "--priority", "0")
		if !strings.Contains(out, "Updated issue") {
			t.Errorf("a real scalar change (priority 2->0) must print '✓ Updated issue'; got:\n%s", out)
		}

		after := showUpdatedAt(t, issue.ID)
		if after == before {
			t.Errorf("a real scalar change MUST bump updated_at: before=%q after=%q", before, after)
		}
	})

	// GUARD 2: a same-value scalar combined with --append-notes is a real
	// mutation (append is non-idempotent) — it must fall through to the write,
	// bump updated_at, and report "✓ Updated" (the skip must not fire on a
	// mixed/non-scalar update).
	t.Run("noop_scalar_with_append_notes_still_bumps_updated_at", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "Noop scalar with notes", "--type", "task")
		before := showUpdatedAt(t, issue.ID)

		out := bdUpdate(t, bd, dir, issue.ID, "--status", "open", "--append-notes", "a real note")
		if !strings.Contains(out, "Updated issue") {
			t.Errorf("a same-value scalar with --append-notes must still print '✓ Updated issue' (the note is real); got:\n%s", out)
		}

		after := showUpdatedAt(t, issue.ID)
		if after == before {
			t.Errorf("--append-notes is a real mutation and MUST bump updated_at even with a same-value scalar: before=%q after=%q", before, after)
		}
	})
}
