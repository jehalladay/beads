//go:build cgo

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestEmbeddedImportCustomStatusValidation_efq11 is the beads-efq11 teeth: the
// batch-create / import validation path must accept a row whose status is a
// legitimately-configured CUSTOM status.
//
// ROOT: issueops.GetCustomStatusesTx (internal/storage/issueops/helpers.go)
// read the raw `status.custom` config string and did a naive JSON/comma split
// that did NOT strip the ":category" suffix, so a stored value like
// "resolved:done,parked:frozen" produced the literal names
// ["resolved:done","parked:frozen"]. NewBatchContext (create.go) feeds those to
// Issue.ValidateWithCustom, so any batch/import row carrying a plain custom
// status ("resolved"/"parked") was rejected as "invalid status: resolved".
// The direct single-update path uses the category-aware
// ResolveCustomStatusesDetailedInTx, which is why `bd update --status resolved`
// works but `bd import` of the same status did not.
//
// FIX: GetCustomStatusesTx delegates to ResolveCustomStatusesDetailedInTx +
// types.CustomStatusNames (the exact pattern EmbeddedDoltStore.GetCustomStatuses
// already uses), so the import path sees the SAME parsed names.
//
// Driven END-TO-END through the real `bd import` subprocess. MUTATION-VERIFIED:
// revert GetCustomStatusesTx to the naive split (keep the ":category" suffix)
// → both subtests go RED (import rejects the plain custom-status rows).
func TestEmbeddedImportCustomStatusValidation_efq11(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)

	// (1) A NEW imported row carrying a configured custom DONE-category status
	//     ("resolved") must be accepted and stored with that status. This is a
	//     standalone leaf (no children), so no close-guard is in play — the only
	//     gate is the batch validation that efq11 fixes.
	t.Run("import_new_row_with_custom_done_status_accepted", func(t *testing.T) {
		t.Parallel()
		dir, _, _ := bdInit(t, bd, "--prefix", "efd")
		bdConfig(t, bd, dir, "set", "status.custom", "resolved:done,parked:frozen")

		jsonl := filepath.Join(t.TempDir(), "new-resolved.jsonl")
		writeRawJSONL(t, jsonl, fmt.Sprintf(
			`{"id":"efd-1","title":"resolved leaf","issue_type":"task","status":"resolved","updated_at":%q}`,
			"2027-06-01T00:00:00Z"))
		bdImport(t, bd, dir, jsonl)

		got := bdShow(t, bd, dir, "efd-1")
		if got.Status != types.Status("resolved") {
			t.Errorf("beads-efq11: import of a configured custom done-category status was rejected/altered, got status %q — GetCustomStatusesTx must strip the :category suffix", got.Status)
		}
	})

	// (2) Same for a configured custom FROZEN-category status ("parked") — the
	//     category-blind reader rejected every custom status, not just done ones.
	t.Run("import_new_row_with_custom_frozen_status_accepted", func(t *testing.T) {
		t.Parallel()
		dir, _, _ := bdInit(t, bd, "--prefix", "eff")
		bdConfig(t, bd, dir, "set", "status.custom", "resolved:done,parked:frozen")

		jsonl := filepath.Join(t.TempDir(), "new-parked.jsonl")
		writeRawJSONL(t, jsonl, fmt.Sprintf(
			`{"id":"eff-1","title":"parked leaf","issue_type":"task","status":"parked","updated_at":%q}`,
			"2027-06-01T00:00:00Z"))
		bdImport(t, bd, dir, jsonl)

		got := bdShow(t, bd, dir, "eff-1")
		if got.Status != types.Status("parked") {
			t.Errorf("beads-efq11: import of a configured custom frozen-category status was rejected/altered, got status %q", got.Status)
		}
	})
}
