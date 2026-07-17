package sqlbuild

import (
	"strings"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/types"
)

// joinClauses is a tiny helper so a test can assert a fragment appears in the
// generated WHERE stack regardless of position.
func hasClause(clauses []string, substr string) bool {
	for _, c := range clauses {
		if strings.Contains(c, substr) {
			return true
		}
	}
	return false
}

func strPtr(s string) *string { return &s }
func intPtr(i int) *int       { return &i }
func boolPtr(b bool) *bool    { return &b }

func TestBuildIssueFilterClauses_Query(t *testing.T) {
	t.Parallel()

	t.Run("issue-id-like query uses id/title/external_ref", func(t *testing.T) {
		where, args, err := BuildIssueFilterClauses("bd-123", types.IssueFilter{}, IssuesFilterTables)
		if err != nil {
			t.Fatal(err)
		}
		if !hasClause(where, "id = ?") || !hasClause(where, "external_ref") {
			t.Fatalf("expected issue-id branch, got %v", where)
		}
		if len(args) != 4 {
			t.Fatalf("args=%v, want 4", args)
		}
	})

	t.Run("free-text query uses title/id LIKE", func(t *testing.T) {
		where, args, err := BuildIssueFilterClauses("some words here", types.IssueFilter{}, IssuesFilterTables)
		if err != nil {
			t.Fatal(err)
		}
		if !hasClause(where, "LOWER(title) LIKE ? OR id LIKE ?") {
			t.Fatalf("expected free-text branch, got %v", where)
		}
		if len(args) != 2 || args[0] != "%some words here%" {
			t.Fatalf("args=%v, want two %%...%% patterns", args)
		}
	})
}

func TestBuildIssueFilterClauses_ScalarFilters(t *testing.T) {
	t.Parallel()
	status := types.StatusOpen
	itype := types.TypeBug

	f := types.IssueFilter{
		TitleSearch:         "ts",
		TitleContains:       "tc",
		DescriptionContains: "dc",
		NotesContains:       "nc",
		ExternalRefContains: "er",
		Status:              &status,
		Statuses:            []types.Status{types.StatusOpen, types.StatusClosed},
		ExcludeStatus:       []types.Status{types.StatusClosed},
		IssueType:           &itype,
		ExcludeTypes:        []types.IssueType{types.TypeBug},
		Assignee:            strPtr("alice"),
		Priority:            intPtr(2),
		PriorityMin:         intPtr(1),
		PriorityMax:         intPtr(3),
	}
	where, args, err := BuildIssueFilterClauses("", f, IssuesFilterTables)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"LOWER(title) LIKE ?", "LOWER(description) LIKE ?", "LOWER(notes) LIKE ?",
		"LOWER(external_ref) LIKE ?", "status = ?", "status IN (?,?)",
		"status NOT IN (?)", "issue_type = ?", "issue_type NOT IN (?)",
		"assignee = ?", "priority = ?", "priority >= ?", "priority <= ?",
	} {
		if !hasClause(where, want) {
			t.Errorf("missing clause %q in %v", want, where)
		}
	}
	if len(args) == 0 {
		t.Fatal("expected args")
	}
}

func TestBuildIssueFilterClauses_IDsLabelsBooleans(t *testing.T) {
	t.Parallel()
	f := types.IssueFilter{
		IDs:           []string{"bd-1", "bd-2"},
		IDPrefix:      "bd-",
		SpecIDPrefix:  "S-",
		Labels:        []string{"backend", "urgent"},
		LabelsAny:     []string{"a", "b"},
		ExcludeLabels: []string{"wontfix"},
		NoLabels:      false,
		Pinned:        boolPtr(true),
		SourceRepo:    strPtr("repo"),
		Ephemeral:     boolPtr(false),
		IsTemplate:    boolPtr(true),
	}
	where, _, err := BuildIssueFilterClauses("", f, IssuesFilterTables)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"id IN (?, ?)", "id LIKE ?", "spec_id LIKE ?",
		"SELECT issue_id FROM labels WHERE label = ?",
		"SELECT issue_id FROM labels WHERE label IN (?, ?)",
		"id NOT IN (SELECT issue_id FROM labels WHERE label IN (?))",
		"pinned = 1", "source_repo = ?",
		"(ephemeral = 0 OR ephemeral IS NULL)", "is_template = 1",
	} {
		if !hasClause(where, want) {
			t.Errorf("missing clause %q in %v", want, where)
		}
	}
}

func TestBuildIssueFilterClauses_ParentAndEmptiness(t *testing.T) {
	t.Parallel()
	f := types.IssueFilter{
		ParentID:         strPtr("bd-parent"),
		EmptyDescription: true,
		NoAssignee:       true,
	}
	where, _, err := BuildIssueFilterClauses("", f, IssuesFilterTables)
	if err != nil {
		t.Fatal(err)
	}
	if !hasClause(where, "parent-child") {
		t.Errorf("expected parent-child subquery, got %v", where)
	}
	if !hasClause(where, "(description IS NULL OR description = '')") {
		t.Errorf("expected empty-description clause, got %v", where)
	}
	if !hasClause(where, "(assignee IS NULL OR assignee = '')") {
		t.Errorf("expected no-assignee clause, got %v", where)
	}

	// NoParent + NoLabels + unpinned/non-ephemeral/non-template negatives.
	f2 := types.IssueFilter{
		NoParent:   true,
		NoLabels:   true,
		Pinned:     boolPtr(false),
		Ephemeral:  boolPtr(true),
		IsTemplate: boolPtr(false),
	}
	where2, _, err := BuildIssueFilterClauses("", f2, IssuesFilterTables)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"id NOT IN (SELECT issue_id FROM dependencies WHERE type = 'parent-child')",
		"id NOT IN (SELECT DISTINCT issue_id FROM labels)",
		"(pinned = 0 OR pinned IS NULL)", "ephemeral = 1",
		"(is_template = 0 OR is_template IS NULL)",
	} {
		if !hasClause(where2, want) {
			t.Errorf("missing clause %q in %v", want, where2)
		}
	}
}

func TestBuildIssueFilterClauses_TimeAndStateFilters(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	f := types.IssueFilter{
		CreatedAfter:  &now,
		CreatedBefore: &now,
		UpdatedAfter:  &now,
		DueBefore:     &now,
		Deferred:      true,
		Overdue:       true,
	}
	where, args, err := BuildIssueFilterClauses("", f, IssuesFilterTables)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"created_at > ?", "created_at < ?", "updated_at > ?", "due_at < ?",
		"(defer_until IS NOT NULL OR status = ?)",
		"due_at IS NOT NULL AND due_at < ? AND status != ?",
	} {
		if !hasClause(where, want) {
			t.Errorf("missing clause %q in %v", want, where)
		}
	}
	if len(args) == 0 {
		t.Fatal("expected time args")
	}
}

func TestBuildIssueFilterClauses_Metadata(t *testing.T) {
	t.Parallel()

	t.Run("valid metadata key and fields", func(t *testing.T) {
		f := types.IssueFilter{
			HasMetadataKey: "gc.routed_to",
			MetadataFields: map[string]string{"team": "backend", "area": "sql"},
		}
		where, args, err := BuildIssueFilterClauses("", f, IssuesFilterTables)
		if err != nil {
			t.Fatal(err)
		}
		if !hasClause(where, "JSON_EXTRACT(metadata, ?) IS NOT NULL") {
			t.Errorf("expected has-key clause, got %v", where)
		}
		if !hasClause(where, "JSON_UNQUOTE(JSON_EXTRACT(metadata, ?)) = ?") {
			t.Errorf("expected field-match clause, got %v", where)
		}
		// keys sorted: area before team.
		if args[len(args)-4] != `$.area` {
			t.Errorf("expected sorted keys, args tail=%v", args[len(args)-4:])
		}
	})

	t.Run("invalid has-key rejected", func(t *testing.T) {
		f := types.IssueFilter{HasMetadataKey: "1bad key"}
		if _, _, err := BuildIssueFilterClauses("", f, IssuesFilterTables); err == nil {
			t.Fatal("expected metadata key validation error")
		}
	})

	t.Run("invalid field key rejected", func(t *testing.T) {
		f := types.IssueFilter{MetadataFields: map[string]string{"bad key": "v"}}
		if _, _, err := BuildIssueFilterClauses("", f, IssuesFilterTables); err == nil {
			t.Fatal("expected metadata field key validation error")
		}
	})
}

func TestAppendMetadataClauses_EmptyIsNoop(t *testing.T) {
	t.Parallel()
	where := []string{"status = ?"}
	args := []any{"open"}
	gotW, gotA, err := AppendMetadataClauses(where, args, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(gotW) != 1 || len(gotA) != 1 {
		t.Fatalf("expected unchanged, got where=%v args=%v", gotW, gotA)
	}
}

func TestBuildLabelDrivenSearch(t *testing.T) {
	t.Parallel()

	t.Run("no labels returns main table and clears nothing", func(t *testing.T) {
		plan := BuildLabelDrivenSearch(types.IssueFilter{Status: nil}, IssuesFilterTables)
		if plan.FromSQL != "issues" || plan.Distinct {
			t.Fatalf("plan=%+v, want FromSQL=issues Distinct=false", plan)
		}
	})

	t.Run("labels produce joins and clear label fields", func(t *testing.T) {
		f := types.IssueFilter{Labels: []string{"backend"}, LabelsAny: []string{"x", "y"}}
		plan := BuildLabelDrivenSearch(f, IssuesFilterTables)
		if !strings.Contains(plan.FromSQL, "JOIN labels label_filter_0") {
			t.Errorf("expected AND-label join, got %q", plan.FromSQL)
		}
		if !strings.Contains(plan.FromSQL, "JOIN labels label_filter_any") {
			t.Errorf("expected any-label join, got %q", plan.FromSQL)
		}
		if !plan.Distinct {
			t.Error("expected Distinct=true")
		}
		if plan.Filter.Labels != nil || plan.Filter.LabelsAny != nil {
			t.Error("expected residual filter to clear label fields")
		}
		if len(plan.Where) != 2 || len(plan.Args) != 3 {
			t.Fatalf("where=%v args=%v, want 2 where / 3 args", plan.Where, plan.Args)
		}
	})
}

func TestLabelSearchPlan_MergeInto(t *testing.T) {
	t.Parallel()

	t.Run("empty plan returns inputs unchanged", func(t *testing.T) {
		p := LabelSearchPlan{}
		w, a := p.MergeInto([]string{"status = ?"}, []any{"open"})
		if len(w) != 1 || len(a) != 1 {
			t.Fatalf("got where=%v args=%v", w, a)
		}
	})

	t.Run("non-empty plan prepends its clauses and args", func(t *testing.T) {
		p := LabelSearchPlan{Where: []string{"l.label = ?"}, Args: []any{"backend"}}
		w, a := p.MergeInto([]string{"status = ?"}, []any{"open"})
		if len(w) != 2 || w[0] != "l.label = ?" || w[1] != "status = ?" {
			t.Fatalf("where=%v, want [l.label=? status=?]", w)
		}
		if len(a) != 2 || a[0] != "backend" || a[1] != "open" {
			t.Fatalf("args=%v, want [backend open]", a)
		}
	})
}

func TestBuildReadyWorkOrder(t *testing.T) {
	t.Parallel()

	t.Run("oldest", func(t *testing.T) {
		o := BuildReadyWorkOrder(types.SortPolicyOldest, "created_at", "priority")
		if o.SQL != "ORDER BY created_at ASC, id ASC" || len(o.Args) != 0 {
			t.Fatalf("o=%+v", o)
		}
	})

	t.Run("priority", func(t *testing.T) {
		o := BuildReadyWorkOrder(types.SortPolicyPriority, "created_at", "priority")
		if o.SQL != "ORDER BY priority ASC, created_at DESC, id ASC" {
			t.Fatalf("sql=%q", o.SQL)
		}
	})

	t.Run("hybrid default has recency cutoff args", func(t *testing.T) {
		o := BuildReadyWorkOrder(types.SortPolicyHybrid, "created_at", "priority")
		if !strings.Contains(o.SQL, "CASE WHEN created_at >= ?") || len(o.Args) != 2 {
			t.Fatalf("o=%+v", o)
		}
		// empty policy takes the same branch
		if e := BuildReadyWorkOrder("", "created_at", "priority"); len(e.Args) != 2 {
			t.Fatalf("empty policy args=%v, want 2", e.Args)
		}
	})

	t.Run("unknown policy falls back to priority", func(t *testing.T) {
		o := BuildReadyWorkOrder(types.SortPolicy("weird"), "sort_created", "sort_priority")
		if o.SQL != "ORDER BY sort_priority ASC, sort_created DESC, id ASC" {
			t.Fatalf("sql=%q", o.SQL)
		}
	})
}

func TestSortKeyCompareAndTimes(t *testing.T) {
	t.Parallel()
	early := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	late := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

	// compareTimesAsc directly.
	if compareTimesAsc(early, late) != -1 {
		t.Error("early<late should be -1")
	}
	if compareTimesAsc(late, early) != 1 {
		t.Error("late>early should be 1")
	}
	if compareTimesAsc(early, early) != 0 {
		t.Error("equal should be 0")
	}

	a := &types.Issue{ID: "bd-1", Priority: 1, CreatedAt: early, UpdatedAt: early, Status: types.StatusOpen, IssueType: types.TypeBug, Assignee: "amy", Title: "Alpha"}
	b := &types.Issue{ID: "bd-2", Priority: 2, CreatedAt: late, UpdatedAt: late, Status: types.StatusClosed, IssueType: types.TypeFeature, Assignee: "bob", Title: "Beta"}

	cases := []struct {
		sortBy string
		want   int // sign of compare(a,b)
	}{
		{"created", -1},
		{"updated", -1},
		{"status", strings.Compare(string(types.StatusOpen), string(types.StatusClosed))},
		{"type", strings.Compare(string(types.TypeBug), string(types.TypeFeature))},
		{"assignee", -1},
		{"title", -1},
		{"priority", -1}, // 1-2
	}
	for _, tc := range cases {
		got := sortKeyCompare(a, b, tc.sortBy)
		if (got < 0) != (tc.want < 0) || (got > 0) != (tc.want > 0) {
			t.Errorf("sortKeyCompare(%q)=%d, want sign of %d", tc.sortBy, got, tc.want)
		}
	}

	// closed: nil-handling (MySQL NULL-first).
	closed := late
	an := &types.Issue{ClosedAt: nil}
	bn := &types.Issue{ClosedAt: &closed}
	if sortKeyCompare(an, bn, "closed") != -1 {
		t.Error("nil closed sorts first (-1)")
	}
	if sortKeyCompare(bn, an, "closed") != 1 {
		t.Error("non-nil vs nil closed = 1")
	}
	if sortKeyCompare(an, an, "closed") != 0 {
		t.Error("both nil closed = 0")
	}
	c2 := early
	bn2 := &types.Issue{ClosedAt: &c2}
	if sortKeyCompare(bn2, bn, "closed") != -1 {
		t.Error("earlier closed sorts first")
	}
}
