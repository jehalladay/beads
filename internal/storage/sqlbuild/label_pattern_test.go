package sqlbuild

import (
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestLabelPatternAndRegexAreApplied pins beads-v5i7: the --label-pattern (glob)
// and --label-regex flags flow into IssueFilter.LabelPattern/LabelRegex, but NO
// query code consumed them — so `bd list --label-pattern tech-*` returned ALL
// issues unfiltered (silent no-op), unlike the working --label exact filter.
// BuildLabelDrivenSearch must emit a labels-table JOIN + predicate for each, so
// the filter actually applies (mirroring the LabelsAny JOIN pattern).
func TestLabelPatternAndRegexAreApplied(t *testing.T) {
	t.Run("label-pattern emits a case-insensitive LIKE JOIN", func(t *testing.T) {
		plan := BuildLabelDrivenSearch(types.IssueFilter{LabelPattern: "tech-*"}, IssuesFilterTables)
		if plan.FromSQL == IssuesFilterTables.Main {
			t.Fatalf("LabelPattern produced no JOIN (FromSQL=%q) — filter is silently ignored", plan.FromSQL)
		}
		w := strings.Join(plan.Where, " AND ")
		if !strings.Contains(strings.ToUpper(w), "LIKE") {
			t.Errorf("LabelPattern did not emit a LIKE predicate: %q", w)
		}
		if !strings.Contains(w, "LOWER(") {
			t.Errorf("LabelPattern LIKE should be case-insensitive (LOWER both sides): %q", w)
		}
		// The glob 'tech-*' must be translated to a SQL LIKE 'tech-%' arg.
		found := false
		for _, a := range plan.Args {
			if s, ok := a.(string); ok && s == "tech-%" {
				found = true
			}
		}
		if !found {
			t.Errorf("glob 'tech-*' should translate to LIKE arg 'tech-%%'; args=%v", plan.Args)
		}
	})

	t.Run("label-regex emits a REGEXP JOIN", func(t *testing.T) {
		plan := BuildLabelDrivenSearch(types.IssueFilter{LabelRegex: "tech-(debt|legacy)"}, IssuesFilterTables)
		if plan.FromSQL == IssuesFilterTables.Main {
			t.Fatalf("LabelRegex produced no JOIN (FromSQL=%q) — filter is silently ignored", plan.FromSQL)
		}
		w := strings.Join(plan.Where, " AND ")
		if !strings.Contains(strings.ToUpper(w), "REGEXP") {
			t.Errorf("LabelRegex did not emit a REGEXP predicate: %q", w)
		}
		found := false
		for _, a := range plan.Args {
			if s, ok := a.(string); ok && s == "tech-(debt|legacy)" {
				found = true
			}
		}
		if !found {
			t.Errorf("regex arg not passed through; args=%v", plan.Args)
		}
	})

	t.Run("no pattern/regex leaves the plan unchanged (no JOIN)", func(t *testing.T) {
		plan := BuildLabelDrivenSearch(types.IssueFilter{}, IssuesFilterTables)
		if plan.FromSQL != IssuesFilterTables.Main {
			t.Errorf("empty filter should not add a JOIN, got FromSQL=%q", plan.FromSQL)
		}
	})
}
