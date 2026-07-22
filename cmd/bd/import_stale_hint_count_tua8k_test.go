package main

import "testing"

// TestStaleSkippedHintCount_tua8k covers beads-tua8k: the "N stale skipped; use
// --allow-stale" human hint was computed as
//
//	result.Skipped - dedupHits - len(result.InvalidMetadataIDs)
//
// but on the non-dry import path result.Skipped only ever holds
// importResult.Skipped (= stale + invalid) — dedupHits is never folded in (the
// dedup rows are filtered out of the batch before importIssuesCore, and the
// lone `result.Skipped = dedupHits` assignment lives in the dry-run branch that
// early-returns before the hint is printed). Subtracting dedupHits therefore
// double-subtracted a value that was never present: the hint under-reported
// stale rows by the dedup count, and when dedup skips >= stale skips it was
// suppressed entirely. The fix counts stale rows from the exact
// StaleSkippedIDs slice (which also matches the JSON stale_skipped_ids field).
func TestStaleSkippedHintCount_tua8k(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		result    importResultJSON
		dedupHits int
		want      int
	}{
		{
			// The reported bug: 2 stale rows AND 3 dedup skips. The old math
			// gave Skipped(2) - dedupHits(3) - invalid(0) = -1 -> hint fully
			// suppressed. Correct answer is 2 stale rows.
			name: "dedup skips >= stale skips no longer suppress the hint",
			result: importResultJSON{
				Skipped:         2, // stale(2) + invalid(0), the non-dry seeding
				StaleSkippedIDs: []string{"bd-1", "bd-2"},
			},
			dedupHits: 3,
			want:      2,
		},
		{
			// Fewer dedup skips than stale: old math under-reported (5-2=3);
			// correct is the full 5 stale rows.
			name: "some dedup skips no longer subtract from the stale count",
			result: importResultJSON{
				Skipped:         5,
				StaleSkippedIDs: []string{"a", "b", "c", "d", "e"},
			},
			dedupHits: 2,
			want:      5,
		},
		{
			// Stale AND invalid rows both land in Skipped; the stale count must
			// exclude the invalid rows (they get their own line) and ignore
			// dedup entirely.
			name: "invalid-metadata rows are excluded from the stale count",
			result: importResultJSON{
				Skipped:            3, // stale(1) + invalid(2)
				StaleSkippedIDs:    []string{"only-stale"},
				InvalidMetadataIDs: []string{"bad-1", "bad-2"},
			},
			dedupHits: 4,
			want:      1,
		},
		{
			name:   "no stale rows -> zero, hint stays silent",
			result: importResultJSON{Skipped: 0},
			want:   0,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := staleSkippedHintCount(tc.result, tc.dedupHits)
			if got != tc.want {
				t.Fatalf("beads-tua8k: staleSkippedHintCount = %d, want %d", got, tc.want)
			}
		})
	}
}
