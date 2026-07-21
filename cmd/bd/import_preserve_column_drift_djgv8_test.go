package main

import (
	"sort"
	"testing"

	"github.com/steveyegge/beads/internal/storage/issueops"
)

// TestRestoreAbsentColumnsCoverIssueUpsertSet_djgv8 is the DRIFT GUARD for the
// import preserve-on-absent restore (beads-djgv8). restoreAbsentFieldsFromLocal
// is a manual mirror of issueops.issueUpsertColumns (the ON DUPLICATE KEY UPDATE
// write set); when the two drift, a partial newer-ts import silently WIPES every
// upsert column the restore forgot. That is exactly what happened between w258p
// (restored 14) and djgv8 (upsert had grown to ~35 via kalv/lbez/xapi2).
//
// This test forces every column in the upsert set to be CONSCIOUSLY classified
// as either restored-on-absent or deliberately-not-restored. Adding a new column
// to issueUpsertColumns without touching restoreAbsentFieldsFromLocal now fails
// the build here instead of shipping a silent data-loss bug. It is a pure-Go
// unit test (no dolt), so it runs on every gate, not only the cgo teeth.
func TestRestoreAbsentColumnsCoverIssueUpsertSet_djgv8(t *testing.T) {
	upsert := issueops.IssueUpsertColumns()

	var unclassified, doubleClassified []string
	for _, col := range upsert {
		_, restored := restoredUpsertColumns[col]
		_, excluded := notRestoredUpsertColumns[col]
		switch {
		case restored && excluded:
			doubleClassified = append(doubleClassified, col)
		case !restored && !excluded:
			unclassified = append(unclassified, col)
		}
	}

	if len(unclassified) > 0 {
		sort.Strings(unclassified)
		t.Errorf("issueUpsertColumns has %d column(s) NOT classified by the import "+
			"preserve-on-absent restore: %v\n"+
			"Every upsert column must be added to EITHER restoredUpsertColumns (with a "+
			"matching per-field branch in restoreAbsentFieldsFromLocal) OR "+
			"notRestoredUpsertColumns (with a reason). An unclassified column is "+
			"silently WIPED on a partial newer-ts import (beads-djgv8 data-loss class).",
			len(unclassified), unclassified)
	}
	if len(doubleClassified) > 0 {
		sort.Strings(doubleClassified)
		t.Errorf("column(s) in BOTH restoredUpsertColumns and notRestoredUpsertColumns "+
			"(must be exactly one): %v", doubleClassified)
	}

	// The classification sets must not reference columns that are no longer in
	// the upsert set (stale entries mask a real gap and rot silently).
	upsertSet := make(map[string]struct{}, len(upsert))
	for _, c := range upsert {
		upsertSet[c] = struct{}{}
	}
	var staleRestored, staleExcluded []string
	for c := range restoredUpsertColumns {
		if _, ok := upsertSet[c]; !ok {
			staleRestored = append(staleRestored, c)
		}
	}
	for c := range notRestoredUpsertColumns {
		if _, ok := upsertSet[c]; !ok {
			staleExcluded = append(staleExcluded, c)
		}
	}
	if len(staleRestored) > 0 {
		sort.Strings(staleRestored)
		t.Errorf("restoredUpsertColumns references column(s) not in issueUpsertColumns "+
			"(stale — drop them): %v", staleRestored)
	}
	if len(staleExcluded) > 0 {
		sort.Strings(staleExcluded)
		t.Errorf("notRestoredUpsertColumns references column(s) not in issueUpsertColumns "+
			"(stale — drop them): %v", staleExcluded)
	}
}
