package format

import (
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

func TestCompactIssue_Open(t *testing.T) {
	issue := &types.Issue{
		ID:        "gas-1",
		Title:     "Open task",
		Status:    types.StatusOpen,
		Priority:  1,
		IssueType: "task",
		Assignee:  "mayor",
	}
	got := CompactIssue(issue, []string{"gt:task"}, nil, nil, "")

	for _, want := range []string{"gas-1", "Open task", "@mayor", "gt:task"} {
		if !strings.Contains(got, want) {
			t.Errorf("CompactIssue(open) missing %q in %q", want, got)
		}
	}
}

func TestCompactIssue_WithDependencies(t *testing.T) {
	issue := &types.Issue{
		ID:        "gas-2",
		Title:     "Blocked task",
		Status:    types.StatusOpen,
		Priority:  2,
		IssueType: "task",
	}
	got := CompactIssue(issue, nil, []string{"gas-9"}, []string{"gas-3"}, "gas-parent")

	for _, want := range []string{"parent: gas-parent", "blocked by: gas-9", "blocks: gas-3"} {
		if !strings.Contains(got, want) {
			t.Errorf("CompactIssue(deps) missing %q in %q", want, got)
		}
	}
}

func TestCompactIssue_ClosedAndPinned(t *testing.T) {
	issue := &types.Issue{
		ID:        "gas-3",
		Title:     "Done and pinned",
		Status:    types.StatusClosed,
		Priority:  0,
		IssueType: "bug",
		Pinned:    true,
	}
	got := CompactIssue(issue, nil, nil, nil, "")

	if !strings.Contains(got, "📌") {
		t.Errorf("CompactIssue(pinned) missing pin prefix in %q", got)
	}
	for _, want := range []string{"gas-3", "Done and pinned", "[P0]", "[bug]"} {
		if !strings.Contains(got, want) {
			t.Errorf("CompactIssue(closed) missing %q in %q", want, got)
		}
	}
}

func TestCompactIssue_OpenNoExtras(t *testing.T) {
	// Open, no assignee/labels/deps — exercises the empty-branch paths.
	issue := &types.Issue{
		ID:        "gas-4",
		Title:     "Bare",
		Status:    types.StatusOpen,
		Priority:  3,
		IssueType: "task",
	}
	got := CompactIssue(issue, nil, nil, nil, "")
	if strings.Contains(got, "@") {
		t.Errorf("CompactIssue(no assignee) should not render '@': %q", got)
	}
	if !strings.Contains(got, "gas-4") || !strings.Contains(got, "Bare") {
		t.Errorf("CompactIssue(bare) missing id/title: %q", got)
	}
}

func TestLongIssue_ClosedAndPinned(t *testing.T) {
	issue := &types.Issue{
		ID:          "gas-5",
		Title:       "Closed detail",
		Status:      types.StatusClosed,
		Priority:    0,
		IssueType:   "bug",
		Pinned:      true,
		Assignee:    "vice",
		Description: "why it closed",
	}
	got := LongIssue(issue, []string{"area:core"})

	for _, want := range []string{"📌", "gas-5", "Closed detail", "vice", "area:core", "why it closed"} {
		if !strings.Contains(got, want) {
			t.Errorf("LongIssue(closed/pinned) missing %q in %q", want, got)
		}
	}
}
