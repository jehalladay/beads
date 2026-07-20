package issueops

import (
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/types"
)

func TestSortSearchIssuesWithCounts(t *testing.T) {
	t.Parallel()

	iwc := func(id string, created time.Time) *types.IssueWithCounts {
		return &types.IssueWithCounts{Issue: &types.Issue{ID: id, CreatedAt: created}}
	}
	t0 := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	t1 := t0.Add(time.Hour)

	t.Run("single element is a no-op", func(t *testing.T) {
		t.Parallel()
		items := []*types.IssueWithCounts{iwc("a", t0)}
		sortSearchIssuesWithCounts(items, "created", false)
		if items[0].Issue.ID != "a" {
			t.Fatalf("single-element sort mutated order: %v", items[0].Issue.ID)
		}
	})

	t.Run("sorts by created (default DESC = newest first)", func(t *testing.T) {
		t.Parallel()
		items := []*types.IssueWithCounts{iwc("old", t0), iwc("new", t1)}
		sortSearchIssuesWithCounts(items, "created", false)
		if items[0].Issue.ID != "new" || items[1].Issue.ID != "old" {
			t.Fatalf("created default = [%s %s], want [new old] (created defaults DESC)", items[0].Issue.ID, items[1].Issue.ID)
		}
	})

	t.Run("created with sortDesc flips to ascending (oldest first)", func(t *testing.T) {
		t.Parallel()
		items := []*types.IssueWithCounts{iwc("new", t1), iwc("old", t0)}
		sortSearchIssuesWithCounts(items, "created", true)
		if items[0].Issue.ID != "old" || items[1].Issue.ID != "new" {
			t.Fatalf("created sortDesc = [%s %s], want [old new]", items[0].Issue.ID, items[1].Issue.ID)
		}
	})

	t.Run("nil-Issue entries sort after real ones", func(t *testing.T) {
		t.Parallel()
		items := []*types.IssueWithCounts{{Issue: nil}, iwc("real", t0)}
		sortSearchIssuesWithCounts(items, "created", false)
		if items[0].Issue == nil || items[0].Issue.ID != "real" {
			t.Fatalf("expected real issue first, got %+v", items[0])
		}
	})
}

func TestSortIssuesWithCountsByPolicy(t *testing.T) {
	t.Parallel()

	iwc := func(id string, prio int, created time.Time) *types.IssueWithCounts {
		return &types.IssueWithCounts{Issue: &types.Issue{ID: id, Priority: prio, CreatedAt: created}}
	}
	base := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)

	t.Run("single element no-op", func(t *testing.T) {
		t.Parallel()
		items := []*types.IssueWithCounts{iwc("a", 0, base)}
		sortIssuesWithCountsByPolicy(items, types.SortPolicyPriority)
		if items[0].Issue.ID != "a" {
			t.Fatal("single-element mutated")
		}
	})

	t.Run("priority policy reorders wrapped items", func(t *testing.T) {
		t.Parallel()
		items := []*types.IssueWithCounts{iwc("p3", 3, base), iwc("p0", 0, base)}
		sortIssuesWithCountsByPolicy(items, types.SortPolicyPriority)
		if items[0].Issue.ID != "p0" || items[1].Issue.ID != "p3" {
			t.Fatalf("priority policy = [%s %s], want [p0 p3]", items[0].Issue.ID, items[1].Issue.ID)
		}
	})

	t.Run("bails (leaves order) when any Issue is nil", func(t *testing.T) {
		t.Parallel()
		items := []*types.IssueWithCounts{iwc("p3", 3, base), {Issue: nil}}
		sortIssuesWithCountsByPolicy(items, types.SortPolicyPriority)
		if items[0].Issue == nil || items[0].Issue.ID != "p3" {
			t.Fatalf("expected untouched order when a nil Issue is present, got %+v", items[0])
		}
	})
}

func TestResolveDependencyTarget(t *testing.T) {
	t.Parallel()

	valid := func(s string) sql.NullString { return sql.NullString{Valid: true, String: s} }
	invalid := sql.NullString{}

	tests := []struct {
		name                    string
		issueT, wispT, external sql.NullString
		wantVal                 string
		wantOK                  bool
	}{
		{name: "issue target wins", issueT: valid("bd-1"), wispT: valid("w-1"), external: valid("ext"), wantVal: "bd-1", wantOK: true},
		{name: "wisp target when no issue", issueT: invalid, wispT: valid("w-1"), external: valid("ext"), wantVal: "w-1", wantOK: true},
		{name: "external when no issue/wisp", issueT: invalid, wispT: invalid, external: valid("ext"), wantVal: "ext", wantOK: true},
		{name: "all invalid -> empty,false", issueT: invalid, wispT: invalid, external: invalid, wantVal: "", wantOK: false},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			val, ok := resolveDependencyTarget(tt.issueT, tt.wispT, tt.external)
			if val != tt.wantVal || ok != tt.wantOK {
				t.Fatalf("resolveDependencyTarget = (%q,%v), want (%q,%v)", val, ok, tt.wantVal, tt.wantOK)
			}
		})
	}
}

func TestBuildReadyWorkOrder(t *testing.T) {
	t.Parallel()

	t.Run("oldest orders by created asc, id", func(t *testing.T) {
		t.Parallel()
		o := buildReadyWorkOrder(types.SortPolicyOldest)
		if !strings.Contains(o.SQL, "created_at ASC") || !strings.Contains(o.SQL, "id ASC") {
			t.Fatalf("oldest SQL = %q", o.SQL)
		}
		if len(o.Args) != 0 {
			t.Fatalf("oldest should carry no args, got %v", o.Args)
		}
	})

	t.Run("priority orders by priority asc, created desc", func(t *testing.T) {
		t.Parallel()
		o := buildReadyWorkOrder(types.SortPolicyPriority)
		if !strings.Contains(o.SQL, "priority ASC") || !strings.Contains(o.SQL, "created_at DESC") {
			t.Fatalf("priority SQL = %q", o.SQL)
		}
	})

	t.Run("hybrid carries two recency-cutoff args", func(t *testing.T) {
		t.Parallel()
		o := buildReadyWorkOrder(types.SortPolicyHybrid)
		if len(o.Args) != 2 {
			t.Fatalf("hybrid should carry 2 args, got %v", o.Args)
		}
		if !strings.Contains(o.SQL, "CASE WHEN") {
			t.Fatalf("hybrid SQL missing CASE: %q", o.SQL)
		}
	})

	t.Run("empty policy defaults to hybrid", func(t *testing.T) {
		t.Parallel()
		o := buildReadyWorkOrder("")
		if len(o.Args) != 2 || !strings.Contains(o.SQL, "CASE WHEN") {
			t.Fatalf("empty policy should behave like hybrid, got SQL=%q args=%v", o.SQL, o.Args)
		}
	})
}

func TestReadyWorkWispIssueFilter(t *testing.T) {
	t.Parallel()

	t.Run("defaults: pinned false, open+in_progress, exclude infra types", func(t *testing.T) {
		t.Parallel()
		got := readyWorkWispIssueFilter(types.WorkFilter{})
		if got.Pinned == nil || *got.Pinned {
			t.Errorf("Pinned = %v, want false", got.Pinned)
		}
		if len(got.Statuses) != 2 {
			t.Errorf("Statuses = %v, want [open in_progress]", got.Statuses)
		}
		if len(got.ExcludeTypes) == 0 {
			t.Errorf("expected ExcludeTypes populated by default")
		}
	})

	t.Run("explicit status overrides default status set", func(t *testing.T) {
		t.Parallel()
		got := readyWorkWispIssueFilter(types.WorkFilter{Status: types.StatusClosed})
		if got.Status == nil || *got.Status != types.StatusClosed {
			t.Errorf("Status = %v, want closed", got.Status)
		}
		if len(got.Statuses) != 0 {
			t.Errorf("Statuses should be empty when explicit status set, got %v", got.Statuses)
		}
	})

	t.Run("unassigned sets NoAssignee; assignee passes through", func(t *testing.T) {
		t.Parallel()
		gotU := readyWorkWispIssueFilter(types.WorkFilter{Unassigned: true})
		if !gotU.NoAssignee {
			t.Error("expected NoAssignee when Unassigned")
		}
		who := "alice"
		gotA := readyWorkWispIssueFilter(types.WorkFilter{Assignee: &who})
		if gotA.Assignee == nil || *gotA.Assignee != "alice" {
			t.Errorf("Assignee = %v, want alice", gotA.Assignee)
		}
	})

	t.Run("molecule id becomes parent filter", func(t *testing.T) {
		t.Parallel()
		got := readyWorkWispIssueFilter(types.WorkFilter{MoleculeID: "bd-mol"})
		if got.ParentID == nil || *got.ParentID != "bd-mol" {
			t.Errorf("ParentID = %v, want bd-mol", got.ParentID)
		}
	})

	// beads-3y8y8: the wisp tier dropped filters the main ready-issues path
	// (and bd list) honor — priority-min/max, title/desc/notes contains,
	// created/updated/due date ranges, overdue, no-labels, empty-description,
	// label glob/regex. Because wispFilter flows through the standard
	// IssueFilter SQL builder (sqlbuild/filter.go honors all of these), NOT
	// forwarding them made `bd ready --include-ephemeral` silently return
	// wisps that violate those filters (asymmetric with the main path — same
	// class as mz2p LabelsAny). Forward every field the IssueFilter builder
	// supports so the wisp tier honors identical filter semantics.
	t.Run("beads-3y8y8: parity filters forwarded to wisp IssueFilter", func(t *testing.T) {
		t.Parallel()
		pmin, pmax := 1, 3
		ca := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		cb := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
		ua := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
		ub := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
		da := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
		db := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
		got := readyWorkWispIssueFilter(types.WorkFilter{
			PriorityMin:         &pmin,
			PriorityMax:         &pmax,
			TitleContains:       "alpha",
			DescriptionContains: "beta",
			NotesContains:       "gamma",
			CreatedAfter:        &ca,
			CreatedBefore:       &cb,
			UpdatedAfter:        &ua,
			UpdatedBefore:       &ub,
			DueAfter:            &da,
			DueBefore:           &db,
			Overdue:             true,
			NoLabels:            true,
			EmptyDescription:    true,
			LabelPattern:        "tech-*",
			LabelRegex:          "tech-(debt|legacy)",
		})
		if got.PriorityMin == nil || *got.PriorityMin != pmin {
			t.Errorf("PriorityMin = %v, want %d", got.PriorityMin, pmin)
		}
		if got.PriorityMax == nil || *got.PriorityMax != pmax {
			t.Errorf("PriorityMax = %v, want %d", got.PriorityMax, pmax)
		}
		if got.TitleContains != "alpha" {
			t.Errorf("TitleContains = %q, want alpha", got.TitleContains)
		}
		if got.DescriptionContains != "beta" {
			t.Errorf("DescriptionContains = %q, want beta", got.DescriptionContains)
		}
		if got.NotesContains != "gamma" {
			t.Errorf("NotesContains = %q, want gamma", got.NotesContains)
		}
		if got.CreatedAfter == nil || !got.CreatedAfter.Equal(ca) {
			t.Errorf("CreatedAfter = %v, want %v", got.CreatedAfter, ca)
		}
		if got.CreatedBefore == nil || !got.CreatedBefore.Equal(cb) {
			t.Errorf("CreatedBefore = %v, want %v", got.CreatedBefore, cb)
		}
		if got.UpdatedAfter == nil || !got.UpdatedAfter.Equal(ua) {
			t.Errorf("UpdatedAfter = %v, want %v", got.UpdatedAfter, ua)
		}
		if got.UpdatedBefore == nil || !got.UpdatedBefore.Equal(ub) {
			t.Errorf("UpdatedBefore = %v, want %v", got.UpdatedBefore, ub)
		}
		if got.DueAfter == nil || !got.DueAfter.Equal(da) {
			t.Errorf("DueAfter = %v, want %v", got.DueAfter, da)
		}
		if got.DueBefore == nil || !got.DueBefore.Equal(db) {
			t.Errorf("DueBefore = %v, want %v", got.DueBefore, db)
		}
		if !got.Overdue {
			t.Error("Overdue not forwarded")
		}
		if !got.NoLabels {
			t.Error("NoLabels not forwarded")
		}
		if !got.EmptyDescription {
			t.Error("EmptyDescription not forwarded")
		}
		if got.LabelPattern != "tech-*" {
			t.Errorf("LabelPattern = %q, want tech-*", got.LabelPattern)
		}
		if got.LabelRegex != "tech-(debt|legacy)" {
			t.Errorf("LabelRegex = %q, want tech-(debt|legacy)", got.LabelRegex)
		}
	})
}
