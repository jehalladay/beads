package sqlbuild

import (
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestLabelMatchIsCaseInsensitive pins beads-hqp8: label matching must LOWER()
// both sides so the SQL filter/search path agrees with the predicate path
// (query.buildLabelPredicate uses strings.EqualFold). Without this, `label=Bug`
// returned a different set in a simple filter query than in an OR/complex
// predicate query (confirmed against live embedded-dolt).
func TestLabelMatchIsCaseInsensitive(t *testing.T) {
	t.Run("ExcludeLabels lowers both sides", func(t *testing.T) {
		clauses, _, err := BuildIssueFilterClauses("", types.IssueFilter{ExcludeLabels: []string{"WontFix"}}, IssuesFilterTables)
		if err != nil {
			t.Fatal(err)
		}
		joined := strings.Join(clauses, " ")
		if !strings.Contains(joined, "LOWER(label) IN (LOWER(?))") {
			t.Errorf("ExcludeLabels not case-insensitive: %q", joined)
		}
	})

	t.Run("label-driven plan lowers both sides", func(t *testing.T) {
		plan := BuildLabelDrivenSearch(types.IssueFilter{Labels: []string{"Bug"}, LabelsAny: []string{"Frontend"}}, IssuesFilterTables)
		w := strings.Join(plan.Where, " AND ")
		if !strings.Contains(w, "LOWER(label_filter_0.label) = LOWER(?)") {
			t.Errorf("Labels JOIN not case-insensitive: %q", w)
		}
		if !strings.Contains(w, "LOWER(label_filter_any.label) IN (LOWER(?))") {
			t.Errorf("LabelsAny JOIN not case-insensitive: %q", w)
		}
	})

	// beads-xl4k: assignee has the same filter(SQL)-vs-predicate(EqualFold) case
	// divergence as labels; the filter path must LOWER() both sides too.
	t.Run("Assignee lowers both sides", func(t *testing.T) {
		a := "Alice"
		clauses, _, err := BuildIssueFilterClauses("", types.IssueFilter{Assignee: &a}, IssuesFilterTables)
		if err != nil {
			t.Fatal(err)
		}
		if !hasClause(clauses, "LOWER(assignee) = LOWER(?)") {
			t.Errorf("Assignee filter not case-insensitive: %v", clauses)
		}
	})
}
