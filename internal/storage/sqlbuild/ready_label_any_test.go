package sqlbuild

import (
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestReadyWorkLabelsAnyIsApplied pins beads-mz2p: `bd ready --label-any X,Y`
// plumbs into WorkFilter.LabelsAny (ready.go) and the wisp sub-path honors it
// (readyWorkWispIssueFilter), but BuildReadyWorkWhere — the MAIN ready-issues
// query — only emitted a clause for filter.Labels (AND-set) and never consumed
// LabelsAny. So `bd ready --label-any a,b` silently returned ALL ready issues
// (only wisps were narrowed) — the same flag-vs-consumer parity gap class as
// beads-v5i7 (--label-pattern/--label-regex) and beads-hqp8 (label case).
func TestReadyWorkLabelsAnyIsApplied(t *testing.T) {
	t.Run("LabelsAny emits a case-insensitive OR-set clause", func(t *testing.T) {
		where, args, err := BuildReadyWorkWhere(
			types.WorkFilter{LabelsAny: []string{"Bug", "feature"}},
			IssuesFilterTables, ReadyWorkWhereInputs{})
		if err != nil {
			t.Fatal(err)
		}
		// It must restrict via an IN-subquery over the labels table; without this
		// LabelsAny is silently dropped and every ready issue is returned.
		if !strings.Contains(where, "issue_id FROM "+IssuesFilterTables.Labels+" WHERE LOWER(label) IN") {
			t.Errorf("LabelsAny did not emit an OR-set label subquery (silently ignored): %q", where)
		}
		// Case-insensitive to match the existing filter.Labels clause (beads-xl4k).
		if !strings.Contains(where, "LOWER(label) IN") {
			t.Errorf("LabelsAny match should be case-insensitive (LOWER both sides): %q", where)
		}
		var gotBug, gotFeature bool
		for _, v := range args {
			if v == "Bug" {
				gotBug = true
			}
			if v == "feature" {
				gotFeature = true
			}
		}
		if !gotBug || !gotFeature {
			t.Errorf("LabelsAny args not bound; args=%v", args)
		}
	})

	t.Run("no LabelsAny leaves no OR-set label clause", func(t *testing.T) {
		where, _, err := BuildReadyWorkWhere(types.WorkFilter{}, IssuesFilterTables, ReadyWorkWhereInputs{})
		if err != nil {
			t.Fatal(err)
		}
		// The default identity-exclusion clause uses "NOT IN"; an OR-set inclusion
		// clause ("id IN (SELECT ... LOWER(label) IN") must NOT appear.
		if strings.Contains(where, "id IN (SELECT issue_id FROM "+IssuesFilterTables.Labels+" WHERE LOWER(label) IN") {
			t.Errorf("empty filter should not add an OR-set label inclusion clause: %q", where)
		}
	})
}
