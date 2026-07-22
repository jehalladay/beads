package main

import (
	"context"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/types"
)

// beads-x7946: `bd import --dry-run` over-reported "created" — it counted every
// incoming row as a creation (result.Created = len(issues)) and never consulted
// the change classifier, so a preview of a round-trip re-import claimed to
// create rows that already exist. The root was a DryRun early-return in
// importIssuesCore that bailed BEFORE classification (returning a useless
// all-Skipped result) combined with a CLI dry-run branch that never set
// opts.DryRun at all. The fix runs the same read-only classifier a real import
// uses and skips only the write, so the preview returns the true
// created/updated/tie_kept/unchanged/skipped partition.
//
// These teeth exercise importIssuesCore with DryRun:true against the fake
// lookup store, which records every write into f.created — so a preview that
// still writes (the pre-fix early-return didn't write, but also didn't
// classify) or that miscounts is caught.

// A dry-run over a batch that is entirely a no-op re-import of existing rows
// must report Created=0 (not len(issues)) and must NOT write.
func TestImportIssuesCoreDryRunUnchangedNotCreated_x7946(t *testing.T) {
	base := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	store := &fakeImportIssueLookupStore{issues: []*types.Issue{
		{ID: "bd-a", Title: "already here", UpdatedAt: base},
		{ID: "bd-b", Title: "also here", UpdatedAt: base},
	}}

	result, err := importIssuesCore(context.Background(), "", store, []*types.Issue{
		{ID: "bd-a", Title: "already here", UpdatedAt: base}, // identical => unchanged
		{ID: "bd-b", Title: "also here", UpdatedAt: base},    // identical => unchanged
	}, ImportOptions{DryRun: true})
	if err != nil {
		t.Fatalf("importIssuesCore dry-run: %v", err)
	}

	// MUTATION-VERIFY: the pre-fix CLI counted result.Created = len(issues) = 2;
	// the pre-fix core early-return produced Created=0 but Unchanged=nil (no
	// classification). The fix must yield Created=0 AND the unchanged partition.
	if result.Created != 0 {
		t.Fatalf("beads-x7946: dry-run of an all-unchanged batch must report Created=0, got %d", result.Created)
	}
	if len(result.Unchanged) != 2 {
		t.Fatalf("beads-x7946: dry-run must classify both existing rows as Unchanged; got %#v", result.Unchanged)
	}
	// The preview reports the rows it WOULD land (created ⊆ processed): both
	// idempotent no-ops still "land" as no-op upserts on a real import.
	if len(result.ImportedIDs) != 2 {
		t.Fatalf("beads-x7946: dry-run should still report the rows it would touch; ImportedIDs=%#v", result.ImportedIDs)
	}
	// CRITICAL: a dry-run must not write. The fake store records every write in
	// f.created — it must be empty.
	if len(store.created) != 0 {
		t.Fatalf("beads-x7946: dry-run must NOT write, but the store recorded %d created rows", len(store.created))
	}
}

// A dry-run over a mixed batch (new + update + tie + unchanged) reports the same
// true partition a real import would, and still performs no write.
func TestImportIssuesCoreDryRunMixedPartitionNoWrite_x7946(t *testing.T) {
	base := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	store := &fakeImportIssueLookupStore{issues: []*types.Issue{
		{ID: "bd-same", Title: "identical", UpdatedAt: base},                      // no-op
		{ID: "bd-upd", Title: "old", Status: types.StatusClosed, UpdatedAt: base}, // will update
		{ID: "bd-tie", Title: "t", Notes: "local", UpdatedAt: base},               // same-second tie
	}}

	result, err := importIssuesCore(context.Background(), "", store, []*types.Issue{
		{ID: "bd-same", Title: "identical", UpdatedAt: base},                                   // unchanged
		{ID: "bd-upd", Title: "new", Status: types.StatusOpen, UpdatedAt: base.Add(time.Hour)}, // update
		{ID: "bd-tie", Title: "t", UpdatedAt: base},                                            // tie
		{ID: "bd-fresh", Title: "brand new", UpdatedAt: base},                                  // create
	}, ImportOptions{DryRun: true})
	if err != nil {
		t.Fatalf("importIssuesCore dry-run: %v", err)
	}

	// The preview partition must match the real-import partition exactly.
	if result.Created != 1 {
		t.Fatalf("beads-x7946: only bd-fresh is genuinely new in the preview; Created=%d want 1", result.Created)
	}
	if result.Updated != 1 || len(result.UpdatedIssues) != 1 || result.UpdatedIssues[0].ID != "bd-upd" {
		t.Fatalf("beads-x7946: bd-upd is the sole previewed update; Updated=%d UpdatedIssues=%#v", result.Updated, result.UpdatedIssues)
	}
	if len(result.TieKeptLocalIDs) != 1 || result.TieKeptLocalIDs[0] != "bd-tie" {
		t.Fatalf("beads-x7946: bd-tie is the sole previewed tie; TieKeptLocalIDs=%#v", result.TieKeptLocalIDs)
	}
	if len(result.Unchanged) != 1 || result.Unchanged[0] != "bd-same" {
		t.Fatalf("beads-x7946: bd-same is the sole previewed unchanged no-op; Unchanged=%#v", result.Unchanged)
	}
	// created + updated + tie + unchanged partitions the previewed landed rows.
	if got := result.Created + result.Updated + len(result.TieKeptLocalIDs) + len(result.Unchanged); got != len(result.ImportedIDs) {
		t.Fatalf("beads-x7946: preview partition %d must equal previewed landed rows %d", got, len(result.ImportedIDs))
	}
	// No write on a dry-run.
	if len(store.created) != 0 {
		t.Fatalf("beads-x7946: dry-run must NOT write, but the store recorded %d created rows", len(store.created))
	}
}

// A real import (DryRun:false) over the same mixed batch DOES write — proving
// the DryRun guard gates only the write, not the classification, and that the
// non-dry path is unchanged.
func TestImportIssuesCoreNonDryRunStillWrites_x7946(t *testing.T) {
	base := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	store := &fakeImportIssueLookupStore{issues: []*types.Issue{
		{ID: "bd-upd", Title: "old", Status: types.StatusClosed, UpdatedAt: base},
	}}

	result, err := importIssuesCore(context.Background(), "", store, []*types.Issue{
		{ID: "bd-upd", Title: "new", Status: types.StatusOpen, UpdatedAt: base.Add(time.Hour)},
		{ID: "bd-fresh", Title: "brand new", UpdatedAt: base},
	}, ImportOptions{}) // DryRun defaults to false
	if err != nil {
		t.Fatalf("importIssuesCore: %v", err)
	}
	if result.Created != 1 || result.Updated != 1 {
		t.Fatalf("beads-x7946: non-dry import Created=%d Updated=%d want 1/1", result.Created, result.Updated)
	}
	// A real import must have written both rows.
	if len(store.created) != 2 {
		t.Fatalf("beads-x7946: non-dry import must write; store recorded %d created rows, want 2", len(store.created))
	}
}
