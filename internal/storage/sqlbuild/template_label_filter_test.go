package sqlbuild

import (
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// beads-82fas: `list --template` (and `query template=true`, which routes
// through IssueFilter.IsTemplate) must match template-ness by the is_template
// COLUMN OR the template LABEL. The column is written only by formula-cooked
// protos; a canonical `bd create --label template` proto has is_template=NULL,
// so a column-only clause silently dropped it — even though bd show renders it
// as a template (beads-pcttr). These pure builder tests pin the label leg.
func TestBuildIssueFilterClauses_TemplateLabelLeg(t *testing.T) {
	labelSub := "id IN (SELECT issue_id FROM labels WHERE LOWER(label) = 'template')"

	t.Run("template=true includes the label leg", func(t *testing.T) {
		v := true
		where, _, err := BuildIssueFilterClauses("", types.IssueFilter{IsTemplate: &v}, IssuesFilterTables)
		if err != nil {
			t.Fatalf("BuildIssueFilterClauses: %v", err)
		}
		want := "(is_template = 1 OR " + labelSub + ")"
		if !hasClause(where, want) {
			t.Errorf("template=true should OR the label leg; want %q, got %v", want, where)
		}
		// The bare column-only clause (no label leg) must be gone — check for the
		// exact old clause, not a substring (the new clause contains "is_template
		// = 1" inside the OR).
		for _, c := range where {
			if c == "is_template = 1" {
				t.Errorf("template=true must not emit the bare column-only clause, got %v", where)
			}
		}
	})

	t.Run("template=false negates both column and label", func(t *testing.T) {
		v := false
		where, _, err := BuildIssueFilterClauses("", types.IssueFilter{IsTemplate: &v}, IssuesFilterTables)
		if err != nil {
			t.Fatalf("BuildIssueFilterClauses: %v", err)
		}
		want := "((is_template = 0 OR is_template IS NULL) AND NOT " + labelSub + ")"
		if !hasClause(where, want) {
			t.Errorf("template=false should exclude label protos too; want %q, got %v", want, where)
		}
	})

	t.Run("wisps filter uses the wisp_labels table", func(t *testing.T) {
		v := true
		where, _, err := BuildIssueFilterClauses("", types.IssueFilter{IsTemplate: &v}, WispsFilterTables)
		if err != nil {
			t.Fatalf("BuildIssueFilterClauses: %v", err)
		}
		joined := strings.Join(where, " | ")
		if !strings.Contains(joined, "SELECT issue_id FROM wisp_labels") {
			t.Errorf("wisps template filter should join wisp_labels, got %v", where)
		}
	})

	t.Run("nil IsTemplate emits no template clause", func(t *testing.T) {
		where, _, err := BuildIssueFilterClauses("", types.IssueFilter{}, IssuesFilterTables)
		if err != nil {
			t.Fatalf("BuildIssueFilterClauses: %v", err)
		}
		for _, c := range where {
			if strings.Contains(c, "is_template") {
				t.Errorf("nil IsTemplate should emit no is_template clause, got %v", where)
			}
		}
	})
}
