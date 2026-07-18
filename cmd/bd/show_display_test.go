package main

import (
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/types"
)

// TestSingleIssueSnapshot_DetectsContentChangeAtSameTimestamp is the beads-wuk9
// regression: `bd show --watch` diffs singleIssueSnapshot between polls, but
// updated_at is DATETIME (whole-second granularity), so a field edit landing in
// the SAME second as the prior poll leaves updated_at unchanged. Before the fix
// the snapshot was ID:Status:UpdatedAt only, so such an edit was invisible and
// the watch never refreshed. Including content_hash makes any content change
// detectable regardless of timing. This asserts the same-second case
// deterministically (the embedded test only caught it by timing luck).
func TestSingleIssueSnapshot_DetectsContentChangeAtSameTimestamp(t *testing.T) {
	t.Parallel()
	ts := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	before := &types.Issue{ID: "bd-1", Status: types.StatusOpen, Title: "original", UpdatedAt: ts}
	before.ContentHash = before.ComputeContentHash()

	// Same ID/status/updated_at, but the title (content) changed — exactly the
	// same-second field edit that updated_at cannot distinguish.
	after := &types.Issue{ID: "bd-1", Status: types.StatusOpen, Title: "edited", UpdatedAt: ts}
	after.ContentHash = after.ComputeContentHash()

	if singleIssueSnapshot(before) == singleIssueSnapshot(after) {
		t.Errorf("snapshot must change on a content edit even when updated_at is identical (wuk9): before=%q after=%q",
			singleIssueSnapshot(before), singleIssueSnapshot(after))
	}
}

// TestSingleIssueSnapshot_StableForIdenticalIssue confirms the snapshot does not
// spuriously change for an unchanged issue (no false "changed" on every poll).
func TestSingleIssueSnapshot_StableForIdenticalIssue(t *testing.T) {
	t.Parallel()
	ts := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	iss := &types.Issue{ID: "bd-1", Status: types.StatusOpen, Title: "x", UpdatedAt: ts}
	iss.ContentHash = iss.ComputeContentHash()

	same := &types.Issue{ID: "bd-1", Status: types.StatusOpen, Title: "x", UpdatedAt: ts}
	same.ContentHash = same.ComputeContentHash()

	if singleIssueSnapshot(iss) != singleIssueSnapshot(same) {
		t.Errorf("snapshot must be stable for an unchanged issue: %q vs %q",
			singleIssueSnapshot(iss), singleIssueSnapshot(same))
	}
}
