package main

import "testing"

// TestGateDiscoverSummaryLine_ahcyo guards beads-ahcyo: the non-dry-run
// `bd gate discover` summary must report updatedCount (gates actually written),
// NOT matchCount. When updateGateAwaitID fails mid-loop a gate is matched but
// not updated (the loop `continue`s without incrementing updatedCount), so
// matchCount over-reports the number of gates that received a run ID. The
// --json emit already uses updatedCount; this keeps the human summary honest.
//
// MUTATION-VERIFY: change gateDiscoverSummaryLine's non-dry-run branch back to
// matchCount and partial_update_failure_reports_updated_not_matched FAILS
// (it prints "Updated 2" instead of "Updated 1").
func TestGateDiscoverSummaryLine_ahcyo(t *testing.T) {
	t.Run("partial_update_failure_reports_updated_not_matched", func(t *testing.T) {
		// 2 gates matched, only 1 updated (the other errored mid-loop).
		got := gateDiscoverSummaryLine(false, 2, 1)
		want := "Updated 1 gate(s) with discovered run IDs."
		if got != want {
			t.Errorf("non-dry-run summary must report updatedCount\n got: %q\nwant: %q", got, want)
		}
	})

	t.Run("all_updated_reports_full_count", func(t *testing.T) {
		got := gateDiscoverSummaryLine(false, 3, 3)
		want := "Updated 3 gate(s) with discovered run IDs."
		if got != want {
			t.Errorf("got %q want %q", got, want)
		}
	})

	t.Run("dry_run_reports_match_count", func(t *testing.T) {
		// Dry-run updates nothing (updatedCount stays 0); the meaningful preview
		// count is what WOULD be updated = matchCount.
		got := gateDiscoverSummaryLine(true, 2, 0)
		want := "Would update 2 gate(s). Run without --dry-run to apply."
		if got != want {
			t.Errorf("dry-run summary must report matchCount\n got: %q\nwant: %q", got, want)
		}
	})
}
