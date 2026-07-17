package main

import (
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/types"
)

// beads-d3qq: coverage for the pure sort/filter helpers in cmd/bd/list.go
// (compareIssuesBy, sortIssuesWithCounts, withFetchOneExtra,
// readyWorkFilterFromIssueFilter). These are pure functions with no storage
// dependency, so the tests are fully hermetic.

// strPtr is defined in ado_test.go (same package).
func intPtr(i int) *int { return &i }

func TestWithFetchOneExtra(t *testing.T) {
	t.Run("positive limit is incremented", func(t *testing.T) {
		out := withFetchOneExtra(types.IssueFilter{Limit: 5})
		if out.Limit != 6 {
			t.Fatalf("Limit=5 should become 6, got %d", out.Limit)
		}
	})
	t.Run("zero limit is left unchanged", func(t *testing.T) {
		out := withFetchOneExtra(types.IssueFilter{Limit: 0})
		if out.Limit != 0 {
			t.Fatalf("Limit=0 should stay 0 (no cap), got %d", out.Limit)
		}
	})
	t.Run("negative limit is left unchanged", func(t *testing.T) {
		out := withFetchOneExtra(types.IssueFilter{Limit: -1})
		if out.Limit != -1 {
			t.Fatalf("Limit=-1 should stay -1, got %d", out.Limit)
		}
	})
	t.Run("other fields are preserved", func(t *testing.T) {
		in := types.IssueFilter{Limit: 3, Offset: 7, Assignee: strPtr("alice")}
		out := withFetchOneExtra(in)
		if out.Limit != 4 || out.Offset != 7 || out.Assignee == nil || *out.Assignee != "alice" {
			t.Fatalf("unexpected passthrough: %+v", out)
		}
	})
}

func TestReadyWorkFilterFromIssueFilter(t *testing.T) {
	t.Run("scalar and slice fields map across", func(t *testing.T) {
		in := types.IssueFilter{
			Limit:          10,
			Offset:         2,
			Labels:         []string{"a", "b"},
			LabelsAny:      []string{"c"},
			ExcludeLabels:  []string{"d"},
			LabelPattern:   "tech-*",
			LabelRegex:     "tech-(x|y)",
			ParentID:       strPtr("bd-parent"),
			ExcludeTypes:   []types.IssueType{types.IssueType("merge-request")},
			MetadataFields: map[string]string{"k": "v"},
			HasMetadataKey: "flag",
		}
		wf := readyWorkFilterFromIssueFilter(in)

		if wf.Status != types.StatusOpen {
			t.Errorf("Status should be forced to open, got %q", wf.Status)
		}
		if wf.Limit != 10 || wf.Offset != 2 {
			t.Errorf("Limit/Offset mismatch: %d/%d", wf.Limit, wf.Offset)
		}
		if len(wf.Labels) != 2 || len(wf.LabelsAny) != 1 || len(wf.ExcludeLabels) != 1 {
			t.Errorf("label slices mismatch: %+v", wf)
		}
		if wf.LabelPattern != "tech-*" || wf.LabelRegex != "tech-(x|y)" {
			t.Errorf("label pattern/regex mismatch: %+v", wf)
		}
		if wf.ParentID == nil || *wf.ParentID != "bd-parent" {
			t.Errorf("ParentID mismatch: %+v", wf.ParentID)
		}
		if len(wf.ExcludeTypes) != 1 || wf.ExcludeTypes[0] != types.IssueType("merge-request") {
			t.Errorf("ExcludeTypes mismatch: %+v", wf.ExcludeTypes)
		}
		if wf.MetadataFields["k"] != "v" || wf.HasMetadataKey != "flag" {
			t.Errorf("metadata mismatch: %+v", wf)
		}
	})

	t.Run("issue type is stringified when set", func(t *testing.T) {
		it := types.TypeBug
		wf := readyWorkFilterFromIssueFilter(types.IssueFilter{IssueType: &it})
		if wf.Type != string(types.TypeBug) {
			t.Fatalf("Type should be %q, got %q", types.TypeBug, wf.Type)
		}
	})
	t.Run("nil issue type leaves empty Type", func(t *testing.T) {
		wf := readyWorkFilterFromIssueFilter(types.IssueFilter{})
		if wf.Type != "" {
			t.Fatalf("Type should be empty when IssueType nil, got %q", wf.Type)
		}
	})
	t.Run("priority pointer carried across", func(t *testing.T) {
		wf := readyWorkFilterFromIssueFilter(types.IssueFilter{Priority: intPtr(2)})
		if wf.Priority == nil || *wf.Priority != 2 {
			t.Fatalf("Priority mismatch: %+v", wf.Priority)
		}
	})
	t.Run("assignee pointer carried across", func(t *testing.T) {
		wf := readyWorkFilterFromIssueFilter(types.IssueFilter{Assignee: strPtr("bob")})
		if wf.Assignee == nil || *wf.Assignee != "bob" {
			t.Fatalf("Assignee mismatch: %+v", wf.Assignee)
		}
	})
	t.Run("NoAssignee maps to Unassigned", func(t *testing.T) {
		wf := readyWorkFilterFromIssueFilter(types.IssueFilter{NoAssignee: true})
		if !wf.Unassigned {
			t.Fatal("NoAssignee=true should set Unassigned=true")
		}
	})
	t.Run("ephemeral true opts in, nil/false does not", func(t *testing.T) {
		yes := true
		if wf := readyWorkFilterFromIssueFilter(types.IssueFilter{Ephemeral: &yes}); !wf.IncludeEphemeral {
			t.Error("Ephemeral=true should set IncludeEphemeral")
		}
		no := false
		if wf := readyWorkFilterFromIssueFilter(types.IssueFilter{Ephemeral: &no}); wf.IncludeEphemeral {
			t.Error("Ephemeral=false should NOT set IncludeEphemeral")
		}
		if wf := readyWorkFilterFromIssueFilter(types.IssueFilter{}); wf.IncludeEphemeral {
			t.Error("Ephemeral=nil should NOT set IncludeEphemeral")
		}
	})
}

func mkIssue(id string, p int) *types.Issue {
	return &types.Issue{ID: id, Priority: p}
}

func TestCompareIssuesBy(t *testing.T) {
	early := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	late := time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)

	t.Run("priority ascending (lower P is first)", func(t *testing.T) {
		if compareIssuesBy(mkIssue("a", 0), mkIssue("b", 2), "priority") >= 0 {
			t.Fatal("P0 should sort before P2")
		}
	})
	t.Run("created newest-first", func(t *testing.T) {
		a := &types.Issue{CreatedAt: late}
		b := &types.Issue{CreatedAt: early}
		if compareIssuesBy(a, b, "created") >= 0 {
			t.Fatal("newer CreatedAt should sort first")
		}
	})
	t.Run("updated newest-first", func(t *testing.T) {
		a := &types.Issue{UpdatedAt: late}
		b := &types.Issue{UpdatedAt: early}
		if compareIssuesBy(a, b, "updated") >= 0 {
			t.Fatal("newer UpdatedAt should sort first")
		}
	})
	t.Run("closed: both nil equal", func(t *testing.T) {
		if compareIssuesBy(&types.Issue{}, &types.Issue{}, "closed") != 0 {
			t.Fatal("both nil ClosedAt should be equal")
		}
	})
	t.Run("closed: nil sorts after non-nil", func(t *testing.T) {
		closed := &types.Issue{ClosedAt: &late}
		open := &types.Issue{}
		if compareIssuesBy(open, closed, "closed") <= 0 {
			t.Fatal("nil ClosedAt (a) should sort after non-nil (b)")
		}
		if compareIssuesBy(closed, open, "closed") >= 0 {
			t.Fatal("non-nil ClosedAt (a) should sort before nil (b)")
		}
	})
	t.Run("closed: newest-first among non-nil", func(t *testing.T) {
		a := &types.Issue{ClosedAt: &late}
		b := &types.Issue{ClosedAt: &early}
		if compareIssuesBy(a, b, "closed") >= 0 {
			t.Fatal("more recently closed should sort first")
		}
	})
	t.Run("status", func(t *testing.T) {
		a := &types.Issue{Status: types.StatusClosed}
		b := &types.Issue{Status: types.StatusOpen}
		if compareIssuesBy(a, b, "status") == 0 {
			t.Fatal("different statuses should not compare equal")
		}
	})
	t.Run("id natural order", func(t *testing.T) {
		if compareIssuesBy(mkIssue("bd-2", 0), mkIssue("bd-10", 0), "id") >= 0 {
			t.Fatal("bd-2 should sort before bd-10 (natural)")
		}
	})
	t.Run("title case-insensitive", func(t *testing.T) {
		a := &types.Issue{Title: "apple"}
		b := &types.Issue{Title: "Banana"}
		if compareIssuesBy(a, b, "title") >= 0 {
			t.Fatal("apple should sort before Banana case-insensitively")
		}
	})
	t.Run("type", func(t *testing.T) {
		a := &types.Issue{IssueType: types.TypeBug}
		b := &types.Issue{IssueType: types.TypeTask}
		if compareIssuesBy(a, b, "type") == 0 {
			t.Fatal("different types should not compare equal")
		}
	})
	t.Run("assignee", func(t *testing.T) {
		a := &types.Issue{Assignee: "alice"}
		b := &types.Issue{Assignee: "bob"}
		if compareIssuesBy(a, b, "assignee") >= 0 {
			t.Fatal("alice should sort before bob")
		}
	})
	t.Run("unknown sort key returns 0", func(t *testing.T) {
		if compareIssuesBy(mkIssue("a", 0), mkIssue("b", 4), "nonsense") != 0 {
			t.Fatal("unknown sort key should return 0 (stable/no-op)")
		}
	})
}

func TestSortIssuesWithCounts(t *testing.T) {
	iwc := func(id string, p int) *types.IssueWithCounts {
		return &types.IssueWithCounts{Issue: mkIssue(id, p)}
	}

	t.Run("empty sortBy is a no-op (order preserved)", func(t *testing.T) {
		items := []*types.IssueWithCounts{iwc("a", 3), iwc("b", 1)}
		sortIssuesWithCounts(items, "", false)
		if items[0].ID != "a" || items[1].ID != "b" {
			t.Fatal("empty sortBy must not reorder")
		}
	})
	t.Run("sort by priority ascending", func(t *testing.T) {
		items := []*types.IssueWithCounts{iwc("a", 3), iwc("b", 1), iwc("c", 2)}
		sortIssuesWithCounts(items, "priority", false)
		if items[0].ID != "b" || items[1].ID != "c" || items[2].ID != "a" {
			t.Fatalf("priority sort wrong: %s,%s,%s", items[0].ID, items[1].ID, items[2].ID)
		}
	})
	t.Run("reverse flips order", func(t *testing.T) {
		items := []*types.IssueWithCounts{iwc("a", 3), iwc("b", 1), iwc("c", 2)}
		sortIssuesWithCounts(items, "priority", true)
		if items[0].ID != "a" || items[1].ID != "c" || items[2].ID != "b" {
			t.Fatalf("reverse priority sort wrong: %s,%s,%s", items[0].ID, items[1].ID, items[2].ID)
		}
	})
	t.Run("nil-Issue entries sort to the end", func(t *testing.T) {
		items := []*types.IssueWithCounts{
			{Issue: nil},
			iwc("b", 1),
			{Issue: nil},
			iwc("a", 0),
		}
		sortIssuesWithCounts(items, "priority", false)
		// The two real issues come first (P0 then P1); nils trail.
		if items[0].Issue == nil || items[0].ID != "a" {
			t.Fatalf("expected real P0 issue first, got %+v", items[0].Issue)
		}
		if items[1].Issue == nil || items[1].ID != "b" {
			t.Fatalf("expected real P1 issue second, got %+v", items[1].Issue)
		}
		if items[2].Issue != nil || items[3].Issue != nil {
			t.Fatal("nil-Issue entries should sort to the end")
		}
	})
	t.Run("both nil compare equal (no panic)", func(t *testing.T) {
		items := []*types.IssueWithCounts{{Issue: nil}, {Issue: nil}}
		sortIssuesWithCounts(items, "priority", false) // must not panic
		if len(items) != 2 {
			t.Fatal("length changed unexpectedly")
		}
	})
	t.Run("interleaved nils exercise both nil-ordering branches", func(t *testing.T) {
		// slices.SortFunc calls the comparator in an implementation-defined
		// arg order, so to guarantee BOTH `ai==nil && bi!=nil` (return 1) and
		// `bi==nil` (return -1) branches fire, seed real and nil entries
		// interleaved across a slice large enough that the sort must compare
		// pairs in both directions. All real issues must land ahead of nils.
		items := []*types.IssueWithCounts{
			{Issue: nil}, iwc("c", 2), {Issue: nil}, iwc("a", 0), iwc("b", 1), {Issue: nil},
		}
		sortIssuesWithCounts(items, "priority", false)
		wantIDs := []string{"a", "b", "c"}
		for i, want := range wantIDs {
			if items[i].Issue == nil || items[i].ID != want {
				t.Fatalf("position %d: want real issue %q, got %+v", i, want, items[i].Issue)
			}
		}
		for i := len(wantIDs); i < len(items); i++ {
			if items[i].Issue != nil {
				t.Fatalf("position %d should be a nil-Issue entry, got %+v", i, items[i].Issue)
			}
		}
	})
}
