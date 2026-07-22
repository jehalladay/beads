package main

import (
	"context"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/types"
)

// beads-grmih: `bd import --allow-stale` of a byte-identical existing row is a
// no-op upsert — neither a creation nor an overwrite. planAllowStaleChanges
// (added by beads-06x87) only classified an existing row into Updates when
// importRowChangeSummary != ""; an IDENTICAL row (summary == "") fell into
// NEITHER Updates nor Unchanged, so it was absent from nonCreated and the
// created-partition loop counted it. That is the exact beads-fkzvk defect
// (unchanged re-import miscounted as created) reintroduced on the --allow-stale
// twin path. The fix adds the else leg that records an identical row in
// Unchanged, exactly as the guarded filterStaleImportIssues path does.
//
// MUTATION-VERIFY: with the fix reverted (no else leg → plan.Unchanged empty),
// the identical row is absent from nonCreated → Created=1 and Unchanged=nil,
// and both assertions below FAIL. That is the beads-grmih defect.
func TestImportIssuesCoreAllowStaleIdenticalRowIsUnchanged_grmih(t *testing.T) {
	base := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	// Local row is byte-for-byte identical to the incoming one (every field
	// importRowChangeSummary compares matches), so the --allow-stale upsert is
	// a pure no-op. The local updated_at is NEWER to prove this is the
	// allow-stale path (a guarded import would tie-keep/skip, not reach here).
	store := &fakeImportIssueLookupStore{issues: []*types.Issue{
		{ID: "bd-restore", Title: "SAME", UpdatedAt: base.Add(time.Hour)},
	}}

	result, err := importIssuesCore(context.Background(), "", store, []*types.Issue{
		{ID: "bd-restore", Title: "SAME", UpdatedAt: base},
	}, ImportOptions{AllowStale: true})
	if err != nil {
		t.Fatalf("importIssuesCore: %v", err)
	}

	if result.Created != 0 {
		t.Fatalf("beads-grmih: an identical --allow-stale no-op re-import was miscounted as Created=%d, want 0 (it is Unchanged)", result.Created)
	}
	if len(result.Unchanged) != 1 || result.Unchanged[0] != "bd-restore" {
		t.Fatalf("beads-grmih: the identical no-op row must land in Unchanged; got Unchanged=%#v", result.Unchanged)
	}
	// It is not an update either (content is identical).
	if result.Updated != 0 || len(result.UpdatedIssues) != 0 {
		t.Fatalf("beads-grmih: an identical row is not an Update; Updated=%d UpdatedIssues=%#v", result.Updated, result.UpdatedIssues)
	}
	// The row still lands (allow-stale bypasses the stale skip).
	if len(result.ImportedIDs) != 1 || result.ImportedIDs[0] != "bd-restore" {
		t.Fatalf("beads-grmih: the row must still land under --allow-stale; ImportedIDs=%#v", result.ImportedIDs)
	}
}

// A mixed --allow-stale batch partitions all three landed buckets without
// overlap: a differing overwrite is an Update, an identical row is Unchanged,
// and a row with no local counterpart is a Create. created + updated +
// unchanged must account for every landed row (the property beads-06x87
// establishes for created+updated; beads-grmih extends it to include the
// unchanged no-op bucket).
func TestImportIssuesCoreAllowStaleMixedBatchPartitionsWithUnchanged_grmih(t *testing.T) {
	base := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	store := &fakeImportIssueLookupStore{issues: []*types.Issue{
		{ID: "bd-diff", Title: "LOCAL TITLE", UpdatedAt: base.Add(time.Hour)},
		{ID: "bd-same", Title: "IDENTICAL", UpdatedAt: base.Add(time.Hour)},
	}}

	result, err := importIssuesCore(context.Background(), "", store, []*types.Issue{
		{ID: "bd-diff", Title: "RESTORED TITLE", UpdatedAt: base}, // differs -> Update
		{ID: "bd-same", Title: "IDENTICAL", UpdatedAt: base},      // identical -> Unchanged
		{ID: "bd-new", Title: "fresh", UpdatedAt: base},           // no local -> Create
	}, ImportOptions{AllowStale: true})
	if err != nil {
		t.Fatalf("importIssuesCore: %v", err)
	}

	if result.Updated != 1 || len(result.UpdatedIssues) != 1 || result.UpdatedIssues[0].ID != "bd-diff" {
		t.Fatalf("beads-grmih: the differing overwrite must be an Update; Updated=%d UpdatedIssues=%#v", result.Updated, result.UpdatedIssues)
	}
	if len(result.Unchanged) != 1 || result.Unchanged[0] != "bd-same" {
		t.Fatalf("beads-grmih: the identical row must be Unchanged; Unchanged=%#v", result.Unchanged)
	}
	if result.Created != 1 {
		t.Fatalf("beads-grmih: exactly the brand-new row is a create; Created=%d want 1", result.Created)
	}
	if len(result.ImportedIDs) != 3 {
		t.Fatalf("beads-grmih: all three rows land; ImportedIDs=%#v", result.ImportedIDs)
	}
	if result.Created+result.Updated+len(result.Unchanged) != len(result.ImportedIDs) {
		t.Fatalf("beads-grmih: created(%d)+updated(%d)+unchanged(%d) must partition the %d landed rows without overlap",
			result.Created, result.Updated, len(result.Unchanged), len(result.ImportedIDs))
	}
}
