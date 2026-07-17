package jira

import (
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestIssueToTrackerCapsSummary is the teeth for beads-a2lv: a beads title may be
// up to 500 chars, but Jira's summary field caps at maxSummaryLength (255) and
// 400-rejects anything longer, failing the WHOLE create/update push. The pushed
// summary must be truncated with a visible ellipsis marker while the local title
// is left untouched — mirroring the landed ado truncateTitle pattern (beads-z5ys).
func TestIssueToTrackerCapsSummary(t *testing.T) {
	m := &jiraFieldMapper{apiVersion: "3"}
	longTitle := ""
	for i := 0; i < 300; i++ {
		longTitle += "a"
	}
	issue := &types.Issue{Title: longTitle}

	fields := m.IssueToTracker(issue)
	summary, ok := fields["summary"].(string)
	if !ok {
		t.Fatalf("summary field missing or not a string: %v", fields["summary"])
	}
	if len([]rune(summary)) > maxSummaryLength {
		t.Errorf("pushed summary is %d runes, want <= %d", len([]rune(summary)), maxSummaryLength)
	}
	if summary[len(summary)-3:] != "..." {
		t.Errorf("pushed summary should end with the ellipsis marker, got %q", summary[len(summary)-10:])
	}
	// The local title must be untouched (only the pushed copy is capped).
	if len(issue.Title) != 300 {
		t.Errorf("local title was mutated: len=%d, want 300", len(issue.Title))
	}
}

// TestIssueToTrackerShortSummaryUnchanged confirms titles within the cap pass
// through verbatim (no marker, no truncation).
func TestIssueToTrackerShortSummaryUnchanged(t *testing.T) {
	m := &jiraFieldMapper{apiVersion: "3"}
	issue := &types.Issue{Title: "A perfectly reasonable title"}

	fields := m.IssueToTracker(issue)
	if got := fields["summary"]; got != "A perfectly reasonable title" {
		t.Errorf("short summary should pass through unchanged, got %q", got)
	}
}

// TestIssueToTrackerSummaryRuneAware confirms truncation never splits a
// multi-byte rune: a 300-char title of 2-byte 'é' must cap to <= maxSummaryLength
// runes with a valid UTF-8 result.
func TestIssueToTrackerSummaryRuneAware(t *testing.T) {
	m := &jiraFieldMapper{apiVersion: "3"}
	longTitle := ""
	for i := 0; i < 300; i++ {
		longTitle += "é"
	}
	issue := &types.Issue{Title: longTitle}

	fields := m.IssueToTracker(issue)
	summary := fields["summary"].(string)
	if len([]rune(summary)) > maxSummaryLength {
		t.Errorf("rune-aware truncation exceeded cap: %d runes", len([]rune(summary)))
	}
	// Result must be valid UTF-8 (no split rune) — a split 'é' would corrupt the string.
	for i, r := range summary {
		if r == '�' {
			t.Errorf("truncation split a multi-byte rune at byte %d (invalid UTF-8)", i)
		}
	}
}
