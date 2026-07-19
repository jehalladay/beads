package main

import (
	"bufio"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/types"
)

// captureStaleStdout runs fn with os.Stdout redirected to a pipe and returns
// what fn wrote. Pure-Go (no cgo tag) so it gates on the refinery's pure-Go
// build, unlike the embedded-dolt stale tests (beads-phmp).
func captureStaleStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		var sb strings.Builder
		br := bufio.NewReader(r)
		_, _ = io.Copy(&sb, br)
		done <- sb.String()
	}()
	fn()
	_ = w.Close()
	os.Stdout = old
	return <-done
}

func staleFixture(n int) []*types.Issue {
	issues := make([]*types.Issue, n)
	base := time.Now().Add(-60 * 24 * time.Hour)
	for i := range issues {
		issues[i] = &types.Issue{
			ID:        "st-" + string(rune('a'+i)),
			Title:     "stale issue",
			Status:    types.StatusOpen,
			Priority:  1,
			IssueType: types.TypeTask,
			UpdatedAt: base,
		}
	}
	return issues
}

// beads-phmp: when --limit truncates the stale result set, the header must NOT
// present the page size as the true total. The DIRECT sibling of beads-l39v
// (bd list footer) at a different site (stale.go displayStaleIssues).
func TestDisplayStaleIssues_TruncatedHeaderNotFalseTotal(t *testing.T) {
	// 3 shown, limit 3, more exist → truncated.
	out := captureStaleStdout(t, func() {
		displayStaleIssues(staleFixture(3), 30, true, 3)
	})
	// The old buggy header "Stale issues (3 not updated in 30+ days):" asserts a
	// completeness the truncated view cannot back. When truncated, the count
	// must be qualified — it must not read as a bare total.
	if strings.Contains(out, "Stale issues (3 not updated in 30+ days):") {
		t.Errorf("truncated stale output must not present the page size as the total; got header:\n%s", out)
	}
	// It must still tell the user how many it is showing and that more exist.
	if !strings.Contains(out, "Showing") || !strings.Contains(out, "more") {
		t.Errorf("truncated stale output must indicate a partial view (Showing N ... more); got:\n%s", out)
	}
}

// Non-truncated output keeps the plain total header — no regression to the
// normal (small workspace) case.
func TestDisplayStaleIssues_UntruncatedKeepsTotalHeader(t *testing.T) {
	out := captureStaleStdout(t, func() {
		displayStaleIssues(staleFixture(3), 30, false, 50)
	})
	if !strings.Contains(out, "Stale issues (3 not updated in 30+ days):") {
		t.Errorf("untruncated stale output should keep the plain total header; got:\n%s", out)
	}
}
