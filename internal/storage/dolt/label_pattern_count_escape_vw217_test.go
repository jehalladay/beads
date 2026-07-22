package dolt

import (
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestLabelPatternLiteralMetacharsCountE2E is the teeth for beads-vw217: the
// THIRD globToLike consumer that beads-k3xye missed. globToLike backslash-escapes
// a literal '_'/'%'/'\' in a --label-pattern, but that only matches literally if
// the consuming LIKE clause carries ESCAPE '\' — go-mysql-server has NO default
// LIKE escape char. beads-k3xye wired ESCAPE into BuildLabelDrivenSearch
// (labels.go, the PLAIN bd-list path) and BuildReadyWorkWhere (ready.go, bd ready),
// but BuildIssueFilterClauses (sqlbuild/filter.go, the JSON/counts path added by
// beads-tc9m8) also feeds globToLike output into a LIKE clause and lacked ESCAPE.
//
// CountIssues -> CountIssuesInTx -> BuildIssueFilterClauses is the count path that
// exercises that clause (the same builder backs `bd list --json`/`bd count`),
// distinct from SearchIssues which routes through the labels.go JOIN. So k3xye's
// SearchIssues/GetReadyWork teeth passed while this path stayed broken.
//
// MUTATION-VERIFY: drop ESCAPE '\\' from the filter.go label-pattern clause and
// "literal underscore does not wildcard-match" FAILS — with no escape char the
// pattern 'foo_bar' counts both "foo_bar" and "fooXbar".
func TestLabelPatternLiteralMetacharsCountE2E(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx, cancel := testContext(t)
	defer cancel()

	mk := func(id, label string) {
		iss := &types.Issue{ID: id, Title: id, Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask}
		if err := store.CreateIssue(ctx, iss, "tester"); err != nil {
			t.Fatalf("create %q: %v", id, err)
		}
		if err := store.AddLabel(ctx, iss.ID, label, "tester"); err != nil {
			t.Fatalf("label %q: %v", id, err)
		}
	}
	// A label with a literal underscore, and a same-shape decoy where the
	// underscore position holds a different char. A literal-'_' match counts
	// only the first; a wildcard-'_' match would also count the decoy.
	mk("vw-under", "foo_bar")
	mk("vw-decoy", "fooXbar")
	// A label with a literal percent, and a decoy without it.
	mk("vw-pct", "rate%off")
	mk("vw-plain", "rateoff")

	t.Run("count: literal underscore does not wildcard-match", func(t *testing.T) {
		got, err := store.CountIssues(ctx, "", types.IssueFilter{LabelPattern: "foo_bar"})
		if err != nil {
			t.Fatalf("CountIssues(LabelPattern foo_bar): %v", err)
		}
		if got != 1 {
			t.Errorf("count of literal '_' pattern 'foo_bar' = %d, want 1 (a '_' treated as a wildcard also counts fooXbar — the filter.go clause is missing ESCAPE, beads-vw217)", got)
		}
	})

	t.Run("count: literal percent does not wildcard-match", func(t *testing.T) {
		got, err := store.CountIssues(ctx, "", types.IssueFilter{LabelPattern: "rate%off"})
		if err != nil {
			t.Fatalf("CountIssues(LabelPattern rate%%off): %v", err)
		}
		if got != 1 {
			t.Errorf("count of literal '%%' pattern 'rate%%off' = %d, want 1 (a '%%' treated as a wildcard counts every label — filter.go missing ESCAPE)", got)
		}
	})

	t.Run("count: glob '*' still wildcards after the escape fix", func(t *testing.T) {
		got, err := store.CountIssues(ctx, "", types.IssueFilter{LabelPattern: "foo*bar"})
		if err != nil {
			t.Fatalf("CountIssues(LabelPattern foo*bar): %v", err)
		}
		// 'foo*bar' -> 'foo%bar' must count both foo_bar and fooXbar.
		if got != 2 {
			t.Errorf("count of glob 'foo*bar' = %d, want 2 (foo_bar + fooXbar); the ESCAPE fix must not break real globbing", got)
		}
	})
}
