package sqlbuild

import (
	"strings"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/types"
)

// TestBuildReadyWorkWhere_FilterArms exercises the scalar/optional WorkFilter
// arms of BuildReadyWorkWhere that the existing batching/identity-label tests
// don't reach: explicit Status, Priority, explicit Type (vs the default
// NOT IN exclusion), Unassigned, Assignee, and MoleculeID. All are pure SQL
// string/arg assertions — no DB.
func TestBuildReadyWorkWhere_FilterArms(t *testing.T) {
	t.Parallel()

	prio := 1
	assignee := "beads_eng_2"
	filter := types.WorkFilter{
		Status:     types.StatusInProgress,
		Priority:   &prio,
		Type:       "bug",
		MoleculeID: "mol-1",
	}
	where, args, err := BuildReadyWorkWhere(filter, IssuesFilterTables, ReadyWorkWhereInputs{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Explicit status uses the parameterized "status = ?" form (not the
	// default IN list) and contributes the first arg.
	if !strings.Contains(where, "status = ?") {
		t.Errorf("explicit Status should emit 'status = ?', where = %q", where)
	}
	if strings.Contains(where, "status IN (") {
		t.Errorf("explicit Status should NOT emit the default IN list, where = %q", where)
	}
	if len(args) == 0 || args[0] != string(types.StatusInProgress) {
		t.Errorf("first arg should be the status value, got %v", args)
	}
	if !strings.Contains(where, "priority = ?") {
		t.Errorf("Priority should emit 'priority = ?', where = %q", where)
	}
	// Explicit Type uses "issue_type = ?" and suppresses the NOT IN exclusion.
	if !strings.Contains(where, "issue_type = ?") {
		t.Errorf("explicit Type should emit 'issue_type = ?', where = %q", where)
	}
	if strings.Contains(where, "issue_type NOT IN (") {
		t.Errorf("explicit Type should suppress the default NOT IN exclusion, where = %q", where)
	}
	// MoleculeID clause references the parent-child dependency subquery.
	if !strings.Contains(where, "type = 'parent-child'") {
		t.Errorf("MoleculeID should emit a parent-child subquery, where = %q", where)
	}

	// Unassigned arm.
	uf := types.WorkFilter{Unassigned: true}
	uw, _, err := BuildReadyWorkWhere(uf, IssuesFilterTables, ReadyWorkWhereInputs{})
	if err != nil {
		t.Fatalf("unassigned: %v", err)
	}
	if !strings.Contains(uw, "assignee IS NULL OR assignee = ''") {
		t.Errorf("Unassigned should emit the null/empty assignee clause, where = %q", uw)
	}

	// Assignee arm (mutually exclusive with Unassigned).
	af := types.WorkFilter{Assignee: &assignee}
	aw, aargs, err := BuildReadyWorkWhere(af, IssuesFilterTables, ReadyWorkWhereInputs{})
	if err != nil {
		t.Fatalf("assignee: %v", err)
	}
	if !strings.Contains(aw, "assignee = ?") {
		t.Errorf("Assignee should emit 'assignee = ?', where = %q", aw)
	}
	found := false
	for _, a := range aargs {
		if a == assignee {
			found = true
		}
	}
	if !found {
		t.Errorf("assignee value %q should appear in args %v", assignee, aargs)
	}
}

// TestBuildReadyWorkWhere_ParentIDBatches covers the ParentID descendant path,
// including the >QueryBatchSize batching of ParentDescendantIDs and the
// LIKE-prefix parent clause.
func TestBuildReadyWorkWhere_ParentIDBatches(t *testing.T) {
	t.Parallel()

	parent := "epic-1"
	descendants := make([]string, QueryBatchSize+1) // forces 2 batches
	for i := range descendants {
		descendants[i] = "epic-1.child"
	}
	filter := types.WorkFilter{ParentID: &parent}
	where, args, err := BuildReadyWorkWhere(filter, IssuesFilterTables, ReadyWorkWhereInputs{ParentDescendantIDs: descendants})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The LIKE-prefix parent clause (transitive-descendant fallback).
	if !strings.Contains(where, "id LIKE CONCAT(?, '.%')") {
		t.Errorf("ParentID should emit the LIKE-prefix clause, where = %q", where)
	}
	// Two batched "id IN (?..." descendant clauses for 201 IDs.
	if got := strings.Count(where, "id IN (?"); got != 2 {
		t.Errorf("expected 2 batched descendant IN clauses, got %d; where = %q", got, where)
	}
	// Args: 1 (parentID) + 201 (descendants). (Plus the default excluded-type args.)
	wantMin := 1 + len(descendants)
	if len(args) < wantMin {
		t.Errorf("args = %d, want at least %d (parent + descendants)", len(args), wantMin)
	}
}

// TestBuildReadyWorkWhere_MetadataError covers the AppendMetadataClauses error
// propagation path: an invalid metadata key makes BuildReadyWorkWhere return
// an error with nil SQL/args.
func TestBuildReadyWorkWhere_MetadataError(t *testing.T) {
	t.Parallel()

	// An empty-segment / invalid metadata key trips ValidateMetadataKey.
	filter := types.WorkFilter{HasMetadataKey: "bad key with spaces and \"quotes\""}
	where, args, err := BuildReadyWorkWhere(filter, IssuesFilterTables, ReadyWorkWhereInputs{})
	if err == nil {
		t.Fatalf("expected error for invalid metadata key, got where=%q args=%v", where, args)
	}
	if where != "" || args != nil {
		t.Errorf("on error, expected empty SQL and nil args, got where=%q args=%v", where, args)
	}
}

// TestLess_SortColumns drives Less/sortKeyCompare across every sortBy column and
// the descending/sortDesc combinations that the existing TestLessMirrorsOrderBy
// doesn't fully reach.
func TestLess_SortColumns(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	later := base.Add(time.Hour)
	closedEarly := base
	closedLate := later

	mk := func(id string, mut func(*types.Issue)) *types.Issue {
		i := &types.Issue{
			ID:        id,
			Priority:  2,
			Status:    types.StatusOpen,
			IssueType: types.TypeTask,
			Assignee:  "z",
			Title:     "Zzz",
			CreatedAt: base,
			UpdatedAt: base,
		}
		if mut != nil {
			mut(i)
		}
		return i
	}

	t.Run("id_shortcircuit", func(t *testing.T) {
		a := mk("a-1", nil)
		b := mk("a-2", nil)
		if !Less(a, b, "id", false) {
			t.Errorf("Less by id: a-1 < a-2 expected true")
		}
	})

	t.Run("unknown_sortby_falls_back_to_default", func(t *testing.T) {
		// Unknown key routes to SortDefs[""] (priority ASC). p1 < p3 ascending.
		a := mk("a", func(i *types.Issue) { i.Priority = 1 })
		b := mk("b", func(i *types.Issue) { i.Priority = 3 })
		if !Less(a, b, "nonsense-key", false) {
			t.Errorf("unknown sortBy should behave like priority ASC")
		}
	})

	t.Run("created_desc_default", func(t *testing.T) {
		// created DefaultDir=DESC → later sorts first (Less(later, earlier)=true).
		a := mk("a", func(i *types.Issue) { i.CreatedAt = later })
		b := mk("b", func(i *types.Issue) { i.CreatedAt = base })
		if !Less(a, b, "created", false) {
			t.Errorf("created DESC: later should come before earlier")
		}
		// sortDesc flips the default direction.
		if Less(a, b, "created", true) {
			t.Errorf("created with sortDesc should flip: later should NOT come first")
		}
	})

	t.Run("updated", func(t *testing.T) {
		a := mk("a", func(i *types.Issue) { i.UpdatedAt = later })
		b := mk("b", func(i *types.Issue) { i.UpdatedAt = base })
		if !Less(a, b, "updated", false) {
			t.Errorf("updated DESC: later should come before earlier")
		}
	})

	t.Run("closed_nil_arms", func(t *testing.T) {
		// sortKeyCompare "closed" arm: nil sorts -1 (before non-nil), non-nil
		// sorts +1, and nil-nil is a tie. Exercise all three arms; direction is
		// DESC-default so just assert non-panicking, deterministic results.
		withClosed := mk("a", func(i *types.Issue) { i.ClosedAt = &closedLate })
		nilClosed := mk("b", nil) // ClosedAt == nil
		if Less(withClosed, nilClosed, "closed", false) == Less(nilClosed, withClosed, "closed", false) {
			t.Errorf("closed nil vs non-nil should be asymmetric (one strictly before the other)")
		}
		// both nil → tie on closed → CreatedAt equal → id tiebreak a<b.
		nilA := mk("a", nil)
		nilB := mk("b", nil)
		if !Less(nilA, nilB, "closed", false) {
			t.Errorf("both-nil closed should tiebreak to id a<b")
		}
		// non-nil vs non-nil compares the times (earlier vs later, no panic).
		early := mk("a", func(i *types.Issue) { i.ClosedAt = &closedEarly })
		late := mk("b", func(i *types.Issue) { i.ClosedAt = &closedLate })
		_ = Less(early, late, "closed", false)
	})

	t.Run("status", func(t *testing.T) {
		a := mk("a", func(i *types.Issue) { i.Status = types.StatusClosed })
		b := mk("b", func(i *types.Issue) { i.Status = types.StatusOpen })
		// status ASC: "closed" < "open" lexically.
		if !Less(a, b, "status", false) {
			t.Errorf("status ASC: closed should sort before open")
		}
	})

	t.Run("type", func(t *testing.T) {
		a := mk("a", func(i *types.Issue) { i.IssueType = types.TypeBug })
		b := mk("b", func(i *types.Issue) { i.IssueType = types.TypeTask })
		if !Less(a, b, "type", false) {
			t.Errorf("type ASC: bug should sort before task")
		}
	})

	t.Run("assignee", func(t *testing.T) {
		a := mk("a", func(i *types.Issue) { i.Assignee = "alice" })
		b := mk("b", func(i *types.Issue) { i.Assignee = "bob" })
		if !Less(a, b, "assignee", false) {
			t.Errorf("assignee ASC: alice should sort before bob")
		}
	})

	t.Run("title_caseinsensitive", func(t *testing.T) {
		a := mk("a", func(i *types.Issue) { i.Title = "apple" })
		b := mk("b", func(i *types.Issue) { i.Title = "Banana" })
		// title ASC, case-insensitive: apple < banana.
		if !Less(a, b, "title", false) {
			t.Errorf("title ASC case-insensitive: apple should sort before Banana")
		}
	})

	t.Run("priority_created_tiebreak", func(t *testing.T) {
		// Equal priority → the priority/default branch falls to CreatedAt.After
		// tiebreak (newer first).
		newer := mk("a", func(i *types.Issue) { i.Priority = 2; i.CreatedAt = later })
		older := mk("b", func(i *types.Issue) { i.Priority = 2; i.CreatedAt = base })
		if !Less(newer, older, "priority", false) {
			t.Errorf("equal priority should tiebreak to newer CreatedAt first")
		}
	})

	t.Run("full_tie_falls_to_id", func(t *testing.T) {
		a := mk("a-1", nil)
		b := mk("a-2", nil)
		// identical everything except id → id tiebreak.
		if !Less(a, b, "priority", false) {
			t.Errorf("full tie should fall to id a-1 < a-2")
		}
	})
}
