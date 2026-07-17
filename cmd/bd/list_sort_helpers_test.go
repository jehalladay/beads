package main

import (
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/types"
)

// beads-d3qq: hermetic tests for the pure sort/filter helpers in list.go
// (verified 0% + no test refs).

func TestCompareIssuesBy(t *testing.T) {
	early := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	late := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	a := &types.Issue{ID: "bd-1", Priority: 1, CreatedAt: early, UpdatedAt: early, Status: types.StatusOpen, Title: "Apple", IssueType: types.TypeBug, Assignee: "alice"}
	b := &types.Issue{ID: "bd-2", Priority: 3, CreatedAt: late, UpdatedAt: late, Status: types.StatusClosed, Title: "banana", IssueType: types.TypeTask, Assignee: "bob"}

	// priority: lower first → a<b.
	if compareIssuesBy(a, b, "priority") >= 0 {
		t.Error("priority: a(P1) should sort before b(P3)")
	}
	// created/updated: newer first → b (late) before a (early) → compare returns <0 for b vs a.
	if compareIssuesBy(a, b, "created") <= 0 {
		t.Error("created: newer-first means a(early) sorts after b(late)")
	}
	if compareIssuesBy(a, b, "updated") <= 0 {
		t.Error("updated: newer-first")
	}
	// id: natural compare bd-1 < bd-2.
	if compareIssuesBy(a, b, "id") >= 0 {
		t.Error("id: bd-1 < bd-2")
	}
	// title: case-insensitive Apple < banana.
	if compareIssuesBy(a, b, "title") >= 0 {
		t.Error("title: apple < banana (case-insensitive)")
	}
	// status/type/assignee: deterministic non-panic comparisons.
	_ = compareIssuesBy(a, b, "status")
	_ = compareIssuesBy(a, b, "type")
	_ = compareIssuesBy(a, b, "assignee")
	// unknown sort key → 0.
	if compareIssuesBy(a, b, "bogus") != 0 {
		t.Error("unknown sort key should compare equal")
	}
}

func TestCompareIssuesBy_ClosedNilHandling(t *testing.T) {
	closed := late()
	withClosed := &types.Issue{ID: "c", ClosedAt: &closed}
	noClosed := &types.Issue{ID: "o"}

	if compareIssuesBy(noClosed, noClosed, "closed") != 0 {
		t.Error("both nil ClosedAt → equal")
	}
	// nil ClosedAt sorts after a non-nil one (open issues last).
	if compareIssuesBy(noClosed, withClosed, "closed") != 1 {
		t.Error("a=nil, b=set → 1 (a after b)")
	}
	if compareIssuesBy(withClosed, noClosed, "closed") != -1 {
		t.Error("a=set, b=nil → -1 (a before b)")
	}
}

func late() time.Time { return time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC) }

func TestSortIssuesWithCounts(t *testing.T) {
	mk := func(id string, pri int) *types.IssueWithCounts {
		return &types.IssueWithCounts{Issue: &types.Issue{ID: id, Priority: pri}}
	}

	t.Run("empty sortBy is a no-op", func(t *testing.T) {
		items := []*types.IssueWithCounts{mk("b", 3), mk("a", 1)}
		sortIssuesWithCounts(items, "", false)
		if items[0].ID != "b" {
			t.Error("no sort should preserve order")
		}
	})

	t.Run("sort by priority ascending", func(t *testing.T) {
		items := []*types.IssueWithCounts{mk("b", 3), mk("a", 1), mk("c", 2)}
		sortIssuesWithCounts(items, "priority", false)
		if items[0].ID != "a" || items[1].ID != "c" || items[2].ID != "b" {
			t.Errorf("expected a,c,b by priority, got %s,%s,%s", items[0].ID, items[1].ID, items[2].ID)
		}
	})

	t.Run("reverse flips order", func(t *testing.T) {
		items := []*types.IssueWithCounts{mk("b", 3), mk("a", 1)}
		sortIssuesWithCounts(items, "priority", true)
		if items[0].ID != "b" {
			t.Errorf("reverse priority should put P3 first, got %s", items[0].ID)
		}
	})

	t.Run("nil embedded issues sort last", func(t *testing.T) {
		items := []*types.IssueWithCounts{{Issue: nil}, mk("a", 1)}
		sortIssuesWithCounts(items, "priority", false)
		if items[0].ID != "a" {
			t.Error("non-nil issue should sort before a nil one")
		}
	})
}

func TestWithFetchOneExtra(t *testing.T) {
	if got := withFetchOneExtra(types.IssueFilter{Limit: 10}); got.Limit != 11 {
		t.Errorf("limit 10 → %d, want 11", got.Limit)
	}
	if got := withFetchOneExtra(types.IssueFilter{Limit: 0}); got.Limit != 0 {
		t.Errorf("limit 0 (unlimited) should stay 0, got %d", got.Limit)
	}
}

func TestReadyWorkFilterFromIssueFilter(t *testing.T) {
	in := types.IssueFilter{
		Limit:         5,
		Offset:        2,
		Labels:        []string{"urgent"},
		ExcludeLabels: []string{"wontfix"},
		LabelPattern:  "tech-*",
	}
	wf := readyWorkFilterFromIssueFilter(in)
	if wf.Status != types.StatusOpen {
		t.Errorf("ready filter should force Status=open, got %q", wf.Status)
	}
	if wf.Limit != 5 || wf.Offset != 2 {
		t.Errorf("limit/offset not carried: %d/%d", wf.Limit, wf.Offset)
	}
	if len(wf.Labels) != 1 || wf.Labels[0] != "urgent" {
		t.Errorf("labels not carried: %v", wf.Labels)
	}
	if len(wf.ExcludeLabels) != 1 || wf.LabelPattern != "tech-*" {
		t.Errorf("exclude-labels/pattern not carried: %v / %q", wf.ExcludeLabels, wf.LabelPattern)
	}
}
