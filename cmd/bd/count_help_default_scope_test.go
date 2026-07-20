package main

import (
	"strings"
	"testing"
)

// TestCountHelpDocumentsDefaultScope is the teeth for beads-9xt9c: `bd count`
// default EXCLUDES closed/pinned (post-9iia, matching bd list's open-scope
// default), but the --help text still said "returns the total count of issues"
// + the example "# Count all issues" — describing pre-9iia semantics and
// implying it counts everything. This asserts the help now documents the
// open-scope default and points at --status all for the true total, and that
// the misleading "Count all issues" example is gone. Doc-only; guards the
// staleness from silently returning.
func TestCountHelpDocumentsDefaultScope(t *testing.T) {
	long := countCmd.Long

	// Must document the open-scope default and the --status all escape hatch.
	if !strings.Contains(strings.ToLower(long), "open") {
		t.Errorf("count --help Long must document the open-scope default (beads-9xt9c), got:\n%s", long)
	}
	if !strings.Contains(long, "--status all") {
		t.Errorf("count --help Long must point at '--status all' for the true total (beads-9xt9c), got:\n%s", long)
	}

	// The stale, misleading claims must be gone.
	if strings.Contains(long, "# Count all issues") {
		t.Errorf("count --help still has the misleading '# Count all issues' example (beads-9xt9c); default is open-scope")
	}
	if strings.Contains(long, "returns the total count of issues matching the filters") {
		t.Errorf("count --help still claims it 'returns the total count of issues' (beads-9xt9c); default excludes closed/pinned")
	}
}
