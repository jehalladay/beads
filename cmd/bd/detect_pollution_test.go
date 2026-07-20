package main

import (
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestDetectTestPollutionRequiresCoSignal is the beads-77a4e regression (shared
// root with beads-9y89f). detectTestPollution drives BOTH the create-time
// warning AND doctor --check=pollution --clean, which DELETES flagged issues
// from the live DB (backup jsonl first, but --yes skips the prompt) — so a
// false positive here is real data loss, not just an export omission.
//
// Before the fix, a bare test/debug/sample/temp/tmp/benchmark/dummy title
// prefix contributed 0.7 — exactly the >=0.7 inclusion threshold — so a legit
// issue whose title merely started with one of those words landed in the
// medium-confidence (0.7-0.9) bucket and was deleted by --clean with zero
// corroborating evidence. The fix lowers the prefix contribution to 0.6, so a
// prefix match must now co-occur with a corroborating signal (empty/short
// description, sequential ID, rapid batch, or a generic test title) to reach
// the threshold. A prefixed title WITH a substantial description scores 0.6
// alone and is NOT flagged (so --clean does not delete it).
func TestDetectTestPollutionRequiresCoSignal(t *testing.T) {
	realDesc := "This is a genuine, substantial description of real engineering work that clearly is not test pollution."

	// isFlagged reports whether detectTestPollution would classify (and thus
	// --clean would delete) the given issue.
	isFlagged := func(iss *types.Issue) bool {
		for _, p := range detectTestPollution([]*types.Issue{iss}) {
			if p.issue == iss {
				return true
			}
		}
		return false
	}

	// Legit items: scrub-prefix title, but a real (>20 char) description and a
	// non-sequential ID → prefix (0.6) alone is below threshold → NOT flagged →
	// NOT deleted by --clean.
	legit := []*types.Issue{
		{ID: "bd-a1b2c3", Title: "Debug logging subsystem overhaul", Description: realDesc},
		{ID: "bd-d4e5f6", Title: "Sample rate limiter for the ingest API", Description: realDesc},
		{ID: "bd-g7h8i9", Title: "Temp file cleanup on shutdown", Description: realDesc},
		{ID: "bd-j1k2l3", Title: "Test harness parallelization for CI", Description: realDesc},
		{ID: "bd-m4n5o6", Title: "Benchmark tuning for the query planner", Description: realDesc},
		{ID: "bd-p7q8r9", Title: "Dummy account provisioning for demos", Description: realDesc},
	}
	for _, iss := range legit {
		if isFlagged(iss) {
			t.Errorf("beads-77a4e: legit prefixed item with a real description was flagged as pollution (would be deleted by --clean): %q", iss.Title)
		}
	}

	// Genuine pollution: prefix AND a corroborating signal → still flagged, so
	// the co-signal fix does not defang the detector.
	polluted := []struct {
		iss  *types.Issue
		note string
	}{
		{&types.Issue{ID: "bd-s1t2u3", Title: "test-throwaway", Description: ""}, "prefix + no description"},
		{&types.Issue{ID: "bd-v4w5x6", Title: "tmp-scratch", Description: "x"}, "prefix + short description"},
		{&types.Issue{ID: "bd-y7z8a9", Title: "This is a test issue", Description: ""}, "generic test title + no description"},
	}
	for _, tc := range polluted {
		if !isFlagged(tc.iss) {
			t.Errorf("beads-77a4e: genuine pollution (%s) should still be flagged: %q", tc.note, tc.iss.Title)
		}
	}

	// A clean item with no prefix and a real description is never flagged.
	if isFlagged(&types.Issue{ID: "bd-clean1", Title: "Refactor the auth module", Description: realDesc}) {
		t.Error("beads-77a4e: clean non-prefixed item was flagged as pollution")
	}
}
