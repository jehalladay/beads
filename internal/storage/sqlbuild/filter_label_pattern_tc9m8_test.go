package sqlbuild

import (
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestBuildIssueFilterClausesLabelPatternRegex pins beads-tc9m8: the JSON/counts
// path (search_counts.go runFilterSearchQueryInTx, bd count, the dolt in-tx
// readers) builds its WHERE directly from BuildIssueFilterClauses with the FULL
// filter, but that builder never consumed IssueFilter.LabelPattern/LabelRegex —
// only BuildLabelDrivenSearch (the PLAIN `bd list` path) did. So
// `bd list --json --label-pattern <glob>` / `--label-regex <re>` silently
// returned EVERY row unfiltered. This is the JSON-path twin of beads-v5i7 which
// fixed the PLAIN path only. BuildIssueFilterClauses must now emit an id-IN
// subquery for each, so every caller inherits the filter.
func TestBuildIssueFilterClausesLabelPatternRegex(t *testing.T) {
	t.Run("label-pattern emits a case-insensitive LIKE id-IN subquery", func(t *testing.T) {
		where, args, err := BuildIssueFilterClauses("", types.IssueFilter{LabelPattern: "tech-*"}, IssuesFilterTables)
		if err != nil {
			t.Fatal(err)
		}
		w := strings.Join(where, " AND ")
		if !strings.Contains(strings.ToUpper(w), "LIKE") {
			t.Fatalf("beads-tc9m8: LabelPattern produced no LIKE predicate — filter silently ignored on the JSON/counts path: %q", w)
		}
		if !strings.Contains(w, "LOWER(label) LIKE LOWER(?)") {
			t.Errorf("LabelPattern LIKE should be case-insensitive (LOWER both sides): %q", w)
		}
		if !strings.Contains(w, "id IN (SELECT issue_id FROM") {
			t.Errorf("LabelPattern should use an id-IN subquery (no row multiplication on counts): %q", w)
		}
		// The glob 'tech-*' must be translated to a SQL LIKE 'tech-%' arg.
		found := false
		for _, a := range args {
			if s, ok := a.(string); ok && s == "tech-%" {
				found = true
			}
		}
		if !found {
			t.Errorf("glob 'tech-*' should translate to LIKE arg 'tech-%%'; args=%v", args)
		}
	})

	t.Run("label-regex emits a REGEXP id-IN subquery", func(t *testing.T) {
		where, args, err := BuildIssueFilterClauses("", types.IssueFilter{LabelRegex: "tech-(debt|legacy)"}, IssuesFilterTables)
		if err != nil {
			t.Fatal(err)
		}
		w := strings.Join(where, " AND ")
		if !strings.Contains(strings.ToUpper(w), "REGEXP") {
			t.Fatalf("beads-tc9m8: LabelRegex produced no REGEXP predicate — filter silently ignored on the JSON/counts path: %q", w)
		}
		if !strings.Contains(w, "id IN (SELECT issue_id FROM") {
			t.Errorf("LabelRegex should use an id-IN subquery: %q", w)
		}
		found := false
		for _, a := range args {
			if s, ok := a.(string); ok && s == "tech-(debt|legacy)" {
				found = true
			}
		}
		if !found {
			t.Errorf("regex arg not bound; args=%v", args)
		}
	})

	t.Run("both pattern and regex emit two subqueries", func(t *testing.T) {
		where, _, err := BuildIssueFilterClauses("", types.IssueFilter{LabelPattern: "a*", LabelRegex: "b.*"}, IssuesFilterTables)
		if err != nil {
			t.Fatal(err)
		}
		w := strings.Join(where, " AND ")
		if !strings.Contains(strings.ToUpper(w), "LIKE") || !strings.Contains(strings.ToUpper(w), "REGEXP") {
			t.Errorf("both LabelPattern and LabelRegex should emit their clauses: %q", w)
		}
	})

	t.Run("neither flag emits no LIKE/REGEXP from these fields", func(t *testing.T) {
		// A bare filter must not add a label LIKE/REGEXP clause (guards against a
		// spurious always-on subquery). Note: other axes may legitimately use LIKE
		// (title etc.), so this case sets none of them.
		where, _, err := BuildIssueFilterClauses("", types.IssueFilter{}, IssuesFilterTables)
		if err != nil {
			t.Fatal(err)
		}
		w := strings.ToUpper(strings.Join(where, " AND "))
		if strings.Contains(w, "LIKE") || strings.Contains(w, "REGEXP") {
			t.Errorf("unset LabelPattern/LabelRegex must not emit LIKE/REGEXP: %q", w)
		}
	})
}
