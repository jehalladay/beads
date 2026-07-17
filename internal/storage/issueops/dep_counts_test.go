package issueops

import (
	"context"
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/steveyegge/beads/internal/storage/domain"
	"github.com/steveyegge/beads/internal/types"
)

// TestBuildDepTypeClause covers the type-filter clause builder: empty filter
// yields no clause, and a populated filter emits one placeholder per type with
// the string-cast args in order.
func TestBuildDepTypeClause(t *testing.T) {
	t.Parallel()

	t.Run("empty filter yields nothing", func(t *testing.T) {
		clause, args := buildDepTypeClause(nil)
		if clause != "" || args != nil {
			t.Fatalf("empty filter: got (%q, %v), want (\"\", nil)", clause, args)
		}
	})

	t.Run("single type", func(t *testing.T) {
		clause, args := buildDepTypeClause([]types.DependencyType{"blocks"})
		if clause != "type IN (?)" {
			t.Fatalf("clause = %q, want %q", clause, "type IN (?)")
		}
		if len(args) != 1 || args[0] != "blocks" {
			t.Fatalf("args = %v, want [blocks]", args)
		}
	})

	t.Run("multiple types preserve order", func(t *testing.T) {
		clause, args := buildDepTypeClause([]types.DependencyType{"blocks", "parent-child", "waits-for"})
		if clause != "type IN (?,?,?)" {
			t.Fatalf("clause = %q, want %q", clause, "type IN (?,?,?)")
		}
		if len(args) != 3 || args[0] != "blocks" || args[1] != "parent-child" || args[2] != "waits-for" {
			t.Fatalf("args = %v, want [blocks parent-child waits-for]", args)
		}
	})
}

// TestCountDepWhere covers the scalar dep-count helper: the base query, the
// optional type-clause append, and error wrapping with the table name.
func TestCountDepWhere(t *testing.T) {
	t.Parallel()

	t.Run("base query without type clause", func(t *testing.T) {
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery(regexp.QuoteMeta("SELECT count(*) FROM dependencies WHERE issue_id = ?")).
			WithArgs("bd-1").
			WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(int64(4)))
		n, err := countDepWhere(context.Background(), tx, "dependencies", "issue_id = ?", "", "bd-1", nil)
		if err != nil {
			t.Fatalf("countDepWhere: %v", err)
		}
		if n != 4 {
			t.Fatalf("count = %d, want 4", n)
		}
	})

	t.Run("appends type clause and args", func(t *testing.T) {
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery(regexp.QuoteMeta("SELECT count(*) FROM dependencies WHERE issue_id = ? AND type IN (?)")).
			WithArgs("bd-1", "blocks").
			WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(int64(2)))
		n, err := countDepWhere(context.Background(), tx, "dependencies", "issue_id = ?", "type IN (?)", "bd-1", []any{"blocks"})
		if err != nil {
			t.Fatalf("countDepWhere: %v", err)
		}
		if n != 2 {
			t.Fatalf("count = %d, want 2", n)
		}
	})

	t.Run("wraps error with table name", func(t *testing.T) {
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery("SELECT count").WithArgs("bd-1").WillReturnError(errors.New("boom"))
		_, err := countDepWhere(context.Background(), tx, "wisp_dependencies", "issue_id = ?", "", "bd-1", nil)
		if err == nil || err.Error() != "count wisp_dependencies: boom" {
			t.Fatalf("err = %v, want wrapped 'count wisp_dependencies: boom'", err)
		}
	})
}

// TestCountDependencyEdgesInTx covers the direction fan-out: it sums the
// outgoing (issue_id) and/or incoming (target-expr) counts across both dep
// tables per the requested direction, and propagates a query error.
func TestCountDependencyEdgesInTx(t *testing.T) {
	t.Parallel()

	outQ := regexp.QuoteMeta("SELECT count(*) FROM dependencies WHERE issue_id = ?")
	wispOutQ := regexp.QuoteMeta("SELECT count(*) FROM wisp_dependencies WHERE issue_id = ?")
	inQ := regexp.QuoteMeta("SELECT count(*) FROM dependencies WHERE " + DepTargetExpr + " = ?")
	wispInQ := regexp.QuoteMeta("SELECT count(*) FROM wisp_dependencies WHERE " + DepTargetExpr + " = ?")

	t.Run("outgoing only sums both tables", func(t *testing.T) {
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery(outQ).WithArgs("bd-1").WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(int64(3)))
		mock.ExpectQuery(wispOutQ).WithArgs("bd-1").WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(int64(1)))
		n, err := CountDependencyEdgesInTx(context.Background(), tx, "bd-1", domain.DepDirectionOut, nil)
		if err != nil {
			t.Fatalf("CountDependencyEdgesInTx: %v", err)
		}
		if n != 4 {
			t.Fatalf("count = %d, want 4 (3 + 1)", n)
		}
	})

	t.Run("incoming only sums both tables via target expr", func(t *testing.T) {
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery(inQ).WithArgs("bd-1").WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(int64(2)))
		mock.ExpectQuery(wispInQ).WithArgs("bd-1").WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(int64(0)))
		n, err := CountDependencyEdgesInTx(context.Background(), tx, "bd-1", domain.DepDirectionIn, nil)
		if err != nil {
			t.Fatalf("CountDependencyEdgesInTx: %v", err)
		}
		if n != 2 {
			t.Fatalf("count = %d, want 2", n)
		}
	})

	t.Run("both directions with type filter query all four", func(t *testing.T) {
		_, mock, tx := beginMockTx(t)
		// dependencies: out then in; wisp_dependencies: out then in.
		mock.ExpectQuery(regexp.QuoteMeta("SELECT count(*) FROM dependencies WHERE issue_id = ? AND type IN (?)")).
			WithArgs("bd-1", "blocks").WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(int64(1)))
		mock.ExpectQuery(regexp.QuoteMeta("SELECT count(*) FROM dependencies WHERE " + DepTargetExpr + " = ? AND type IN (?)")).
			WithArgs("bd-1", "blocks").WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(int64(2)))
		mock.ExpectQuery(regexp.QuoteMeta("SELECT count(*) FROM wisp_dependencies WHERE issue_id = ? AND type IN (?)")).
			WithArgs("bd-1", "blocks").WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(int64(4)))
		mock.ExpectQuery(regexp.QuoteMeta("SELECT count(*) FROM wisp_dependencies WHERE " + DepTargetExpr + " = ? AND type IN (?)")).
			WithArgs("bd-1", "blocks").WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(int64(8)))
		n, err := CountDependencyEdgesInTx(context.Background(), tx, "bd-1", domain.DepDirectionBoth, []types.DependencyType{"blocks"})
		if err != nil {
			t.Fatalf("CountDependencyEdgesInTx: %v", err)
		}
		if n != 15 {
			t.Fatalf("count = %d, want 15 (1+2+4+8)", n)
		}
	})

	t.Run("propagates query error", func(t *testing.T) {
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery(outQ).WithArgs("bd-1").WillReturnError(errors.New("boom"))
		if _, err := CountDependencyEdgesInTx(context.Background(), tx, "bd-1", domain.DepDirectionOut, nil); err == nil {
			t.Fatal("expected query error, got nil")
		}
	})
}
