package query

import (
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/types"
)

// TestAssigneeTrimParity_sabd pins beads-sabd: the read-side query-lang assignee
// legs must TRIM the value, matching the write side (llzt @7f1b7dae5 stores
// trimmed assignees). Both legs fold case (LOWER/EqualFold) but never trimmed,
// so a padded `assignee="  alice  "` built LOWER(assignee)=LOWER('  alice  ')
// (filter) / EqualFold(i.Assignee, "  alice  ") (predicate) -> no match against
// a stored "alice" -> silent empty (the exact `bd ready --assignee $GT_ROLE`
// never-idle orphaning, from the query side).
func TestAssigneeTrimParity_sabd(t *testing.T) {
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)

	// AND/filter leg: applyAssigneeFilter must set a TRIMMED Filter.Assignee.
	t.Run("filter-trims", func(t *testing.T) {
		res, err := EvaluateAt(`assignee="  alice  "`, now)
		if err != nil {
			t.Fatalf("EvaluateAt(padded assignee) err: %v", err)
		}
		if res.Filter.Assignee == nil {
			t.Fatal("Filter.Assignee is nil; padded assignee filter was dropped")
		}
		if got := *res.Filter.Assignee; got != "alice" {
			t.Errorf("Filter.Assignee = %q, want %q (untrimmed -> unmatchable vs trimmed stored value)", got, "alice")
		}
	})

	// OR/predicate leg: buildAssigneePredicate must match a stored (trimmed)
	// "alice" issue when the query value is padded.
	t.Run("predicate-trims", func(t *testing.T) {
		res, err := EvaluateAt(`assignee="  alice  " OR id=zzz`, now)
		if err != nil {
			t.Fatalf("EvaluateAt(padded assignee OR ...) err: %v", err)
		}
		if res.Predicate == nil {
			t.Fatal("expected a predicate for the OR query")
		}
		if !res.Predicate(&types.Issue{Assignee: "alice"}) {
			t.Error("predicate did not match a stored assignee 'alice' for a padded query value (untrimmed -> silent empty)")
		}
		if res.Predicate(&types.Issue{Assignee: "bob"}) {
			t.Error("predicate matched the wrong assignee")
		}
	})

	// The none/null unassigned sentinel must survive trimming: a padded "none"
	// still means unassigned (must NOT become a literal-assignee search).
	t.Run("padded-none-still-unassigned-filter", func(t *testing.T) {
		res, err := EvaluateAt(`assignee="  none  "`, now)
		if err != nil {
			t.Fatalf("EvaluateAt(padded none) err: %v", err)
		}
		if !res.Filter.NoAssignee {
			t.Error("padded 'none' did not set NoAssignee (unassigned sentinel lost after trim)")
		}
		if res.Filter.Assignee != nil {
			t.Errorf("padded 'none' set a literal Filter.Assignee = %q, want unassigned", *res.Filter.Assignee)
		}
	})
	t.Run("padded-none-still-unassigned-predicate", func(t *testing.T) {
		res, err := EvaluateAt(`assignee="  none  " OR id=zzz`, now)
		if err != nil {
			t.Fatalf("EvaluateAt(padded none OR ...) err: %v", err)
		}
		if res.Predicate == nil {
			t.Fatal("expected a predicate")
		}
		if !res.Predicate(&types.Issue{Assignee: ""}) {
			t.Error("padded 'none' predicate did not match an unassigned issue")
		}
		if res.Predicate(&types.Issue{Assignee: "alice"}) {
			t.Error("padded 'none' predicate matched an assigned issue")
		}
	})
}
