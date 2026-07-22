package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/types"
)

// beads-06x87: `bd import --allow-stale` (the documented "restore an older
// snapshot, overwrites newer local rows" path) must still CLASSIFY an in-place
// overwrite of an existing local issue as an Update — reporting it in Updated /
// UpdatedIssues with a field-level summary, and keeping Created a true
// partition (genuinely-new rows only).
//
// Root cause: the change-PLAN was built only on the guarded (!AllowStale) path
// via filterStaleImportIssues. Under --allow-stale that call was skipped
// entirely, so changePlan stayed zero-valued → Updated=0, UpdatedIssues=nil,
// and every landed row (including overwrites of existing issues) fell through
// to Created. A user restoring an old snapshot got a confidently-wrong
// "created N" count and zero visibility (no updated_issues) into which local
// rows the restore clobbered — exactly the data-observability the help text
// promises. The fix builds the plan on the --allow-stale path too, via
// planAllowStaleChanges (existing + content differs => Update, regardless of
// updated_at direction, since allow-stale always lets the incoming row land).
func TestImportIssuesCoreAllowStaleClassifiesOverwriteAsUpdate_06x87(t *testing.T) {
	base := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	// Local row is NEWER than the incoming one; --allow-stale still overwrites
	// it. The incoming title differs, so this is a genuine content overwrite.
	store := &fakeImportIssueLookupStore{issues: []*types.Issue{
		{ID: "bd-restore", Title: "CLEAN LOCAL", UpdatedAt: base.Add(time.Hour)},
	}}

	result, err := importIssuesCore(context.Background(), "", store, []*types.Issue{
		{ID: "bd-restore", Title: "OLDER SNAPSHOT TITLE", UpdatedAt: base},
	}, ImportOptions{AllowStale: true})
	if err != nil {
		t.Fatalf("importIssuesCore: %v", err)
	}

	// MUTATION-VERIFY: with the fix reverted (the --allow-stale branch not
	// building a plan), changePlan is zero → Updated=0 and Created=1, and both
	// of these assertions FAIL. That is the beads-06x87 defect.
	if result.Updated != 1 || len(result.UpdatedIssues) != 1 || result.UpdatedIssues[0].ID != "bd-restore" {
		t.Fatalf("beads-06x87: an --allow-stale overwrite of an existing issue must be an Update; got Updated=%d UpdatedIssues=%#v", result.Updated, result.UpdatedIssues)
	}
	if !strings.Contains(result.UpdatedIssues[0].Changes, "title") {
		t.Fatalf("beads-06x87: expected a field-level change summary mentioning the title, got %q", result.UpdatedIssues[0].Changes)
	}
	// Created must be a TRUE partition — the overwrite is Updated, NOT Created.
	if result.Created != 0 {
		t.Fatalf("beads-06x87: an --allow-stale overwrite of an existing issue was miscounted as Created=%d, want 0 (it is an Update)", result.Created)
	}
	// The row still lands (allow-stale bypasses the stale skip); it just counts
	// as an update rather than a create.
	if len(result.ImportedIDs) != 1 || result.ImportedIDs[0] != "bd-restore" {
		t.Fatalf("beads-06x87: the row must still land under --allow-stale; ImportedIDs=%#v", result.ImportedIDs)
	}
	if result.Skipped != 0 || len(result.StaleSkippedIDs) != 0 {
		t.Fatalf("beads-06x87: --allow-stale must not skip; Skipped=%d StaleSkippedIDs=%#v", result.Skipped, result.StaleSkippedIDs)
	}
}

// A mixed --allow-stale batch partitions correctly: an existing-issue overwrite
// whose content differs is an Update, and a row with no local counterpart is a
// Create — created + updated together account for every landed row, with no
// double-count. This is the property the guarded (!AllowStale) path already
// holds (TestImportIssuesCoreReportsUpdatedAndTieKeptIssues); beads-06x87
// extends it to the restore path.
func TestImportIssuesCoreAllowStaleMixedBatchPartitions_06x87(t *testing.T) {
	base := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	// bd-existing exists locally (newer) with DIFFERENT content; bd-brandnew has
	// no local counterpart.
	store := &fakeImportIssueLookupStore{issues: []*types.Issue{
		{ID: "bd-existing", Title: "LOCAL TITLE", UpdatedAt: base.Add(time.Hour)},
	}}

	result, err := importIssuesCore(context.Background(), "", store, []*types.Issue{
		{ID: "bd-existing", Title: "RESTORED TITLE", UpdatedAt: base},
		{ID: "bd-brandnew", Title: "fresh", UpdatedAt: base},
	}, ImportOptions{AllowStale: true})
	if err != nil {
		t.Fatalf("importIssuesCore: %v", err)
	}

	// Differing overwrite of an existing row => Update; new row => Create.
	if result.Updated != 1 || len(result.UpdatedIssues) != 1 || result.UpdatedIssues[0].ID != "bd-existing" {
		t.Fatalf("beads-06x87: the differing --allow-stale overwrite must be an Update; Updated=%d UpdatedIssues=%#v", result.Updated, result.UpdatedIssues)
	}
	if result.Created != 1 {
		t.Fatalf("beads-06x87: exactly the brand-new row is a create; Created=%d want 1", result.Created)
	}
	// Partition holds: created + updated == every distinct landed row.
	if len(result.ImportedIDs) != 2 {
		t.Fatalf("beads-06x87: both rows land; ImportedIDs=%#v", result.ImportedIDs)
	}
	if result.Created+result.Updated != len(result.ImportedIDs) {
		t.Fatalf("beads-06x87: created(%d)+updated(%d) must partition the %d landed rows without overlap", result.Created, result.Updated, len(result.ImportedIDs))
	}
}

