package main

import (
	"context"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/types"
)

// beads-fkzvk: an idempotent re-import of an UNCHANGED existing issue (the
// incoming row is byte-for-byte identical to the local one, so the change
// summary is empty) lands as a no-op upsert but must NOT be counted as a
// creation. Before the fix, filterStaleImportIssues only recorded a row in
// Updates (content differs, strictly newer) or TieKeptLocal (content differs,
// same second) — an identical existing row fell into NEITHER, so it was absent
// from the nonCreated set and the created-partition loop counted it as created.
// A round-trip export→import of unchanged data thus reported "created N" while
// ground truth was unchanged, breaking the created/updated/tie_kept/skipped
// partition the y2y8 comment claims to uphold and misleading any restore/sync
// script that trusts created==0 to mean "nothing new".
func TestImportIssuesCoreUnchangedExistingRowIsNotCreated_fkzvk(t *testing.T) {
	base := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	// The incoming row is IDENTICAL to the local row (same id, title, and
	// updated_at) — the exact export→import round-trip of unchanged data.
	local := &types.Issue{ID: "bd-idem", Title: "idempotent test", UpdatedAt: base}
	store := &fakeImportIssueLookupStore{issues: []*types.Issue{local}}

	result, err := importIssuesCore(context.Background(), "", store, []*types.Issue{
		{ID: "bd-idem", Title: "idempotent test", UpdatedAt: base},
	}, ImportOptions{})
	if err != nil {
		t.Fatalf("importIssuesCore: %v", err)
	}

	// MUTATION-VERIFY: without the fix (the else-branch not recording the id in
	// plan.Unchanged, and Created not excluding it), Created==1 and Unchanged is
	// empty — the reported bug. These assertions FAIL against the reverted fix.
	if result.Created != 0 {
		t.Fatalf("beads-fkzvk: a no-op re-import of an unchanged existing issue was miscounted as Created=%d, want 0", result.Created)
	}
	if len(result.Unchanged) != 1 || result.Unchanged[0] != "bd-idem" {
		t.Fatalf("beads-fkzvk: the unchanged existing row must be recorded in Unchanged; got %#v", result.Unchanged)
	}
	// It is neither an update nor a tie conflict — a no-op is not a "kept local
	// on a differing tie" event.
	if result.Updated != 0 || len(result.UpdatedIssues) != 0 {
		t.Fatalf("beads-fkzvk: an unchanged row is not an update; Updated=%d UpdatedIssues=%#v", result.Updated, result.UpdatedIssues)
	}
	if len(result.TieKeptLocalIDs) != 0 {
		t.Fatalf("beads-fkzvk: an unchanged (identical) row is not a tie conflict; TieKeptLocalIDs=%#v", result.TieKeptLocalIDs)
	}
	// The row still lands (the idempotent upsert is a correct no-op write); it
	// is just counted correctly.
	if len(result.ImportedIDs) != 1 || result.ImportedIDs[0] != "bd-idem" {
		t.Fatalf("beads-fkzvk: the row must still be processed; ImportedIDs=%#v", result.ImportedIDs)
	}
	if result.Skipped != 0 || len(result.StaleSkippedIDs) != 0 {
		t.Fatalf("beads-fkzvk: an unchanged re-import is not skipped; Skipped=%d StaleSkippedIDs=%#v", result.Skipped, result.StaleSkippedIDs)
	}
}

// beads-fkzvk / beads-y2y8: a mixed batch partitions cleanly across all four
// landed-row buckets — genuinely new (Created), differing+newer (Updated),
// differing+same-second (TieKeptLocal), and identical (Unchanged) — with no
// double-count. created + updated + tie_kept + unchanged must equal the
// distinct landed rows.
func TestImportIssuesCoreUnchangedRowPartitionsMixedBatch_fkzvk(t *testing.T) {
	base := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	store := &fakeImportIssueLookupStore{issues: []*types.Issue{
		{ID: "bd-same", Title: "identical", UpdatedAt: base},                      // no-op re-import
		{ID: "bd-upd", Title: "old", Status: types.StatusClosed, UpdatedAt: base}, // differs + newer => update
		{ID: "bd-tie", Title: "t", Notes: "local", UpdatedAt: base},               // differs + same second => tie
	}}

	result, err := importIssuesCore(context.Background(), "", store, []*types.Issue{
		{ID: "bd-same", Title: "identical", UpdatedAt: base},                                   // unchanged
		{ID: "bd-upd", Title: "new", Status: types.StatusOpen, UpdatedAt: base.Add(time.Hour)}, // update
		{ID: "bd-tie", Title: "t", UpdatedAt: base},                                            // tie (notes wiped)
		{ID: "bd-fresh", Title: "brand new", UpdatedAt: base},                                  // create
	}, ImportOptions{})
	if err != nil {
		t.Fatalf("importIssuesCore: %v", err)
	}

	if result.Created != 1 {
		t.Fatalf("beads-fkzvk: only bd-fresh is genuinely new; Created=%d want 1", result.Created)
	}
	if result.Updated != 1 || len(result.UpdatedIssues) != 1 || result.UpdatedIssues[0].ID != "bd-upd" {
		t.Fatalf("beads-fkzvk: bd-upd is the sole update; Updated=%d UpdatedIssues=%#v", result.Updated, result.UpdatedIssues)
	}
	if len(result.TieKeptLocalIDs) != 1 || result.TieKeptLocalIDs[0] != "bd-tie" {
		t.Fatalf("beads-fkzvk: bd-tie is the sole tie; TieKeptLocalIDs=%#v", result.TieKeptLocalIDs)
	}
	if len(result.Unchanged) != 1 || result.Unchanged[0] != "bd-same" {
		t.Fatalf("beads-fkzvk: bd-same is the sole unchanged no-op; Unchanged=%#v", result.Unchanged)
	}
	// All four buckets partition the distinct landed rows exactly, no overlap.
	if len(result.ImportedIDs) != 4 {
		t.Fatalf("beads-fkzvk: all four rows land; ImportedIDs=%#v", result.ImportedIDs)
	}
	if got := result.Created + result.Updated + len(result.TieKeptLocalIDs) + len(result.Unchanged); got != len(result.ImportedIDs) {
		t.Fatalf("beads-fkzvk: created(%d)+updated(%d)+tie_kept(%d)+unchanged(%d)=%d must partition the %d landed rows",
			result.Created, result.Updated, len(result.TieKeptLocalIDs), len(result.Unchanged), got, len(result.ImportedIDs))
	}
}
