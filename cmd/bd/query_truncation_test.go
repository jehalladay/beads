package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// beads-ebpo: bd query applied --limit (default 50) then printed "Found N
// issues" as if N were the true match total, silently hiding the rest. These
// pure-Go teeth exercise outputQueryResults directly (the embedded query tests
// are //go:build cgo = invisible to the refinery's pure-Go gate), proving the
// header is qualified when the result set was truncated and unchanged
// otherwise. Same class as beads-l39v/beads-phmp/beads-4wn0.

func queryTruncFixture(n int) []*types.Issue {
	issues := make([]*types.Issue, 0, n)
	for i := 0; i < n; i++ {
		issues = append(issues, &types.Issue{
			ID:        fmt.Sprintf("bd-%05d", i),
			Title:     fmt.Sprintf("issue %d", i),
			Priority:  2,
			IssueType: types.TypeBug,
			Status:    types.StatusOpen,
		})
	}
	return issues
}

func TestOutputQueryResults_TruncatedHeaderNotFalseTotal(t *testing.T) {
	out := captureStdout(t, func() error {
		outputQueryResults(queryTruncFixture(3), "status:open", false, true, 3)
		return nil
	})
	// The bare "Found 3 issues:" total is a lie when more matched; it must be
	// qualified so the page size is not read as the total.
	if strings.Contains(out, "Found 3 issues:") {
		t.Errorf("truncated query printed the bare page size as the total:\n%s", out)
	}
	if !strings.Contains(out, "more") {
		t.Errorf("truncated query header must signal more results exist:\n%s", out)
	}
}

func TestOutputQueryResults_UntruncatedKeepsTotalHeader(t *testing.T) {
	out := captureStdout(t, func() error {
		outputQueryResults(queryTruncFixture(3), "status:open", false, false, 50)
		return nil
	})
	// No regression: an untruncated result set keeps the plain total header.
	if !strings.Contains(out, "Found 3 issues:") {
		t.Errorf("untruncated query lost its total header:\n%s", out)
	}
}
