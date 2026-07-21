//go:build cgo

package main

import (
	"fmt"
	"path/filepath"
	"os"
	"testing"
)

// TestEmbeddedImportPreserveOnAbsent_djgv8 extends the w258p preserve-on-absent
// teeth to the columns w258p's restore list originally MISSED. beads-djgv8: the
// restore set in restoreAbsentFieldsFromLocal (cmd/bd/import_shared.go) covered
// only ~14 of the ~35 columns the import upsert (issueops.issueUpsertColumns,
// the ON DUPLICATE KEY UPDATE write set) rewrites, so a partial newer-ts import
// line silently WIPED the omitted ones — pinned, due_at, defer_until, spec_id,
// closed_at, started_at, closed_by_session, sender, wisp_type, mol_type,
// work_type, waiters, timeout, await_*, event_kind/actor/target/payload, and
// title. These are user-mutable and JSON-exported, so a `bd export → edit one
// line → re-import` round-trip over an existing issue dropped them.
//
// The fix mirrors the full issueUpsertColumns set. This test proves the
// high-value user-facing ones (pinned/due/defer/spec_id + close bookkeeping)
// survive an omitting import, and that an explicit value on the line still
// applies (presence, not value, gates the restore — round-trip stays lossless).
func TestEmbeddedImportPreserveOnAbsent_djgv8(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "dg")

	// Strictly newer than any just-created row, so the stale guard never skips
	// the line and the upsert path under test runs.
	const newerTS = "2027-06-01T00:00:00Z"

	// ===== pinned / due_at / defer_until / spec_id: all set locally, all
	// OMITTED by a newer-ts import line → all must be preserved. =====
	t.Run("absent_scheduling_and_markers_preserve_local", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "keep markers",
			"--type", "task",
			"--spec-id", "SPEC-42",
			"--due", "+2w",
			"--defer", "+1d")
		bdUpdate(t, bd, dir, issue.ID, "--pinned")

		before := showIssue(t, bd, dir, issue.ID)
		if !before.Pinned {
			t.Fatalf("setup: expected pinned=true after --pinned")
		}
		if before.SpecID != "SPEC-42" {
			t.Fatalf("setup: expected spec_id SPEC-42, got %q", before.SpecID)
		}
		if before.DueAt == nil {
			t.Fatalf("setup: expected due_at set")
		}
		if before.DeferUntil == nil {
			t.Fatalf("setup: expected defer_until set")
		}

		jsonl := filepath.Join(t.TempDir(), "m.jsonl")
		// id + title + updated_at only — pinned/spec_id/due_at/defer_until omitted.
		writeRawJSONL(t, jsonl, fmt.Sprintf(
			`{"id":%q,"title":"keep markers","updated_at":%q}`, issue.ID, newerTS))
		bdImport(t, bd, dir, jsonl)

		got := showIssue(t, bd, dir, issue.ID)
		if !got.Pinned {
			t.Errorf("absent pinned was cleared to false — must preserve local (beads-djgv8)")
		}
		if got.SpecID != "SPEC-42" {
			t.Errorf("absent spec_id was cleared to %q — must preserve local (beads-djgv8)", got.SpecID)
		}
		if got.DueAt == nil {
			t.Errorf("absent due_at was cleared to nil — must preserve local (beads-djgv8)")
		}
		if got.DeferUntil == nil {
			t.Errorf("absent defer_until was cleared to nil — must preserve local (beads-djgv8)")
		}
	})

	// ===== close bookkeeping: a closed issue re-imported with a newer-ts line
	// that omits closed_at/close_reason must keep them (not silently re-open the
	// timeline). =====
	t.Run("absent_close_bookkeeping_preserves_local", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "keep close", "--type", "task")
		bdClose(t, bd, dir, issue.ID, "--reason", "shipped")

		before := showIssue(t, bd, dir, issue.ID)
		if before.ClosedAt == nil {
			t.Fatalf("setup: expected closed_at set after close")
		}
		if before.CloseReason != "shipped" {
			t.Fatalf("setup: expected close_reason 'shipped', got %q", before.CloseReason)
		}

		jsonl := filepath.Join(t.TempDir(), "c.jsonl")
		// Keep status closed (so the row stays a closed issue) but omit the close
		// bookkeeping columns.
		writeRawJSONL(t, jsonl, fmt.Sprintf(
			`{"id":%q,"title":"keep close","status":"closed","updated_at":%q}`, issue.ID, newerTS))
		bdImport(t, bd, dir, jsonl)

		got := showIssue(t, bd, dir, issue.ID)
		if got.ClosedAt == nil {
			t.Errorf("absent closed_at was cleared to nil — must preserve local (beads-djgv8)")
		}
		if got.CloseReason != "shipped" {
			t.Errorf("absent close_reason was cleared to %q — must preserve local (beads-djgv8)", got.CloseReason)
		}
	})

	// ===== presence still wins: an EXPLICIT value on the line applies, so the
	// full-fidelity export|import round-trip stays lossless for the new fields
	// too (regression boundary — the restore must not blanket-preserve). =====
	t.Run("explicit_values_still_applied_for_new_fields", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "explicit markers", "--type", "task", "--spec-id", "OLD")
		bdUpdate(t, bd, dir, issue.ID, "--pinned")

		jsonl := filepath.Join(t.TempDir(), "e.jsonl")
		// Explicitly present spec_id (new value) and pinned:false — both must apply.
		writeRawJSONL(t, jsonl, fmt.Sprintf(
			`{"id":%q,"title":"explicit markers","spec_id":"NEW","pinned":false,"updated_at":%q}`, issue.ID, newerTS))
		bdImport(t, bd, dir, jsonl)

		got := showIssue(t, bd, dir, issue.ID)
		if got.SpecID != "NEW" {
			t.Errorf("explicit spec_id NEW not applied, got %q — presence must win (beads-djgv8)", got.SpecID)
		}
		if got.Pinned {
			t.Errorf("explicit pinned:false not applied — presence must win (beads-djgv8)")
		}
	})
}
