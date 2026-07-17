package issueops

import (
	"context"
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/steveyegge/beads/internal/types"
)

func boolPtr(b bool) *bool { return &b }

// TestCountTableInTx covers the scalar table counter: an empty filter yields a
// bare COUNT(*) with no WHERE, and a query error propagates.
func TestCountTableInTx(t *testing.T) {
	t.Parallel()

	t.Run("bare count with empty filter", func(t *testing.T) {
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) FROM issues")).
			WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(9))
		n, err := countTableInTx(context.Background(), tx, "", types.IssueFilter{}, IssuesFilterTables)
		if err != nil {
			t.Fatalf("countTableInTx: %v", err)
		}
		if n != 9 {
			t.Fatalf("count = %d, want 9", n)
		}
	})

	t.Run("query error propagates", func(t *testing.T) {
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery("SELECT COUNT").WillReturnError(errors.New("boom"))
		if _, err := countTableInTx(context.Background(), tx, "", types.IssueFilter{}, IssuesFilterTables); err == nil {
			t.Fatal("expected query error, got nil")
		}
	})
}

// TestCountByColumnInTx covers the grouped column counter: it maps each raw
// column value to its count, and propagates a scan error.
func TestCountByColumnInTx(t *testing.T) {
	t.Parallel()

	q := regexp.QuoteMeta("SELECT COALESCE(status, ''), COUNT(*) FROM issues GROUP BY status")

	t.Run("groups by column value", func(t *testing.T) {
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery(q).WillReturnRows(
			sqlmock.NewRows([]string{"status", "n"}).
				AddRow("open", 5).
				AddRow("closed", 2))
		got, err := countByColumnInTx(context.Background(), tx, types.IssueFilter{}, "status", IssuesFilterTables)
		if err != nil {
			t.Fatalf("countByColumnInTx: %v", err)
		}
		if got["open"] != 5 || got["closed"] != 2 {
			t.Fatalf("counts = %v, want open=5 closed=2", got)
		}
	})

	t.Run("query error propagates", func(t *testing.T) {
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery(q).WillReturnError(errors.New("boom"))
		if _, err := countByColumnInTx(context.Background(), tx, types.IssueFilter{}, "status", IssuesFilterTables); err == nil {
			t.Fatal("expected query error, got nil")
		}
	})
}

// TestCountByLabelInTx covers the label-grouped counter: it counts per-label
// via the subquery form and folds in a "(no labels)" bucket.
func TestCountByLabelInTx(t *testing.T) {
	t.Parallel()

	labelQ := regexp.QuoteMeta("SELECT l.label, COUNT(*) FROM labels l WHERE l.issue_id IN (SELECT id FROM issues) GROUP BY l.label")
	noLabelQ := regexp.QuoteMeta("SELECT COUNT(*) FROM issues WHERE id NOT IN (SELECT DISTINCT issue_id FROM labels)")

	t.Run("labels plus no-label bucket", func(t *testing.T) {
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery(labelQ).WillReturnRows(
			sqlmock.NewRows([]string{"label", "n"}).AddRow("bug", 3).AddRow("infra", 1))
		mock.ExpectQuery(noLabelQ).WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(2))
		got, err := countByLabelInTx(context.Background(), tx, types.IssueFilter{}, IssuesFilterTables)
		if err != nil {
			t.Fatalf("countByLabelInTx: %v", err)
		}
		if got["bug"] != 3 || got["infra"] != 1 || got["(no labels)"] != 2 {
			t.Fatalf("counts = %v, want bug=3 infra=1 (no labels)=2", got)
		}
	})

	t.Run("zero no-label count omits the bucket", func(t *testing.T) {
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery(labelQ).WillReturnRows(
			sqlmock.NewRows([]string{"label", "n"}).AddRow("bug", 3))
		mock.ExpectQuery(noLabelQ).WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(0))
		got, err := countByLabelInTx(context.Background(), tx, types.IssueFilter{}, IssuesFilterTables)
		if err != nil {
			t.Fatalf("countByLabelInTx: %v", err)
		}
		if _, ok := got["(no labels)"]; ok {
			t.Fatalf("counts = %v, want no (no labels) bucket", got)
		}
	})

	t.Run("label query error propagates", func(t *testing.T) {
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery(labelQ).WillReturnError(errors.New("boom"))
		if _, err := countByLabelInTx(context.Background(), tx, types.IssueFilter{}, IssuesFilterTables); err == nil {
			t.Fatal("expected label query error, got nil")
		}
	})
}

// TestCountGroupForTablesInTx covers the group dispatcher: it routes "label" to
// the label counter, maps display groupBy names to columns (with priority "P"
// prefix and unassigned normalization), and rejects an unsupported groupBy.
func TestCountGroupForTablesInTx(t *testing.T) {
	t.Parallel()

	t.Run("priority prefixes P", func(t *testing.T) {
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery(regexp.QuoteMeta("GROUP BY priority")).WillReturnRows(
			sqlmock.NewRows([]string{"priority", "n"}).AddRow("1", 4))
		got, err := countGroupForTablesInTx(context.Background(), tx, types.IssueFilter{}, "priority", IssuesFilterTables)
		if err != nil {
			t.Fatalf("countGroupForTablesInTx: %v", err)
		}
		if got["P1"] != 4 {
			t.Fatalf("counts = %v, want P1=4", got)
		}
	})

	t.Run("assignee normalizes empty to unassigned", func(t *testing.T) {
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery(regexp.QuoteMeta("GROUP BY assignee")).WillReturnRows(
			sqlmock.NewRows([]string{"assignee", "n"}).AddRow("", 3).AddRow("alice", 1))
		got, err := countGroupForTablesInTx(context.Background(), tx, types.IssueFilter{}, "assignee", IssuesFilterTables)
		if err != nil {
			t.Fatalf("countGroupForTablesInTx: %v", err)
		}
		if got["(unassigned)"] != 3 || got["alice"] != 1 {
			t.Fatalf("counts = %v, want (unassigned)=3 alice=1", got)
		}
	})

	t.Run("unsupported groupBy errors", func(t *testing.T) {
		_, _, tx := beginMockTx(t)
		if _, err := countGroupForTablesInTx(context.Background(), tx, types.IssueFilter{}, "bogus", IssuesFilterTables); err == nil {
			t.Fatal("expected unsupported groupBy error, got nil")
		}
	})
}

// TestCountIssuesInTx covers the top-level scalar counter: the default path
// merges the issues + wisps counts, SkipWisps returns issues-only, a missing
// wisps table is tolerated on merge, and the ephemeral-filter branch counts the
// wisps tier.
func TestCountIssuesInTx(t *testing.T) {
	t.Parallel()

	issuesQ := regexp.QuoteMeta("SELECT COUNT(*) FROM issues")
	wispsQ := regexp.QuoteMeta("SELECT COUNT(*) FROM wisps")

	t.Run("merges issues and wisps", func(t *testing.T) {
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery(issuesQ).WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(6))
		mock.ExpectQuery(wispsQ).WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(4))
		n, err := CountIssuesInTx(context.Background(), tx, "", types.IssueFilter{})
		if err != nil {
			t.Fatalf("CountIssuesInTx: %v", err)
		}
		if n != 10 {
			t.Fatalf("count = %d, want 10 (6 + 4)", n)
		}
	})

	t.Run("SkipWisps returns issues only", func(t *testing.T) {
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery(issuesQ).WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(6))
		n, err := CountIssuesInTx(context.Background(), tx, "", types.IssueFilter{SkipWisps: true})
		if err != nil {
			t.Fatalf("CountIssuesInTx: %v", err)
		}
		if n != 6 {
			t.Fatalf("count = %d, want 6", n)
		}
	})

	t.Run("missing wisps table tolerated on merge", func(t *testing.T) {
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery(issuesQ).WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(6))
		mock.ExpectQuery(wispsQ).WillReturnError(tableMissingErr)
		n, err := CountIssuesInTx(context.Background(), tx, "", types.IssueFilter{})
		if err != nil {
			t.Fatalf("CountIssuesInTx: %v", err)
		}
		if n != 6 {
			t.Fatalf("count = %d, want 6 (wisps missing)", n)
		}
	})

	t.Run("ephemeral filter counts wisps tier", func(t *testing.T) {
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery(wispsQ).WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(3))
		n, err := CountIssuesInTx(context.Background(), tx, "", types.IssueFilter{Ephemeral: boolPtr(true)})
		if err != nil {
			t.Fatalf("CountIssuesInTx: %v", err)
		}
		if n != 3 {
			t.Fatalf("count = %d, want 3", n)
		}
	})
}

// TestCountIssuesByGroupInTx covers the top-level grouped counter: the default
// path merges issues + wisps group buckets.
func TestCountIssuesByGroupInTx(t *testing.T) {
	t.Parallel()

	issuesGroup := regexp.QuoteMeta("FROM issues GROUP BY status")
	wispsGroup := regexp.QuoteMeta("FROM wisps GROUP BY status")

	_, mock, tx := beginMockTx(t)
	mock.ExpectQuery(issuesGroup).WillReturnRows(
		sqlmock.NewRows([]string{"status", "n"}).AddRow("open", 5))
	mock.ExpectQuery(wispsGroup).WillReturnRows(
		sqlmock.NewRows([]string{"status", "n"}).AddRow("open", 2))
	got, err := CountIssuesByGroupInTx(context.Background(), tx, types.IssueFilter{}, "status")
	if err != nil {
		t.Fatalf("CountIssuesByGroupInTx: %v", err)
	}
	if got["open"] != 7 {
		t.Fatalf("counts = %v, want open=7 (5 + 2 merged)", got)
	}
}
