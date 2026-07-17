package db

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/go-sql-driver/mysql"
)

// beads-jrjh: sqlmock coverage for three previously-0% DB helpers verified
// uncovered via a real coverage profile: issue_search.go's
// getDependencyRecordsFromTable + scanDepRow, and ready_work.go's
// getDescendantIDs. *sql.DB satisfies Runner, so &issueSQLRepositoryImpl{runner:db}
// exercises them hermetically — no live Dolt.

func newDepMock(t *testing.T) (*issueSQLRepositoryImpl, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return &issueSQLRepositoryImpl{runner: db}, mock
}

var depSelect = regexp.QuoteMeta("SELECT issue_id,")

func TestGetDependencyRecordsFromTable(t *testing.T) {
	ctx := context.Background()

	t.Run("groups rows by issue_id with NULLable columns", func(t *testing.T) {
		r, mock := newDepMock(t)
		cols := []string{"issue_id", "depends_on_id", "type", "created_at", "created_by", "metadata", "thread_id"}
		mock.ExpectQuery(depSelect).WithArgs("a", "b").
			WillReturnRows(sqlmock.NewRows(cols).
				AddRow("a", "x", "blocks", nil, nil, nil, nil).
				AddRow("a", "y", "related", nil, "user", "{}", "th-1").
				AddRow("b", "z", "parent-child", nil, nil, nil, nil))

		got, err := r.getDependencyRecordsFromTable(ctx, "dependencies", []string{"a", "b"})
		if err != nil {
			t.Fatalf("getDependencyRecordsFromTable: %v", err)
		}
		if len(got["a"]) != 2 || len(got["b"]) != 1 {
			t.Fatalf("grouping wrong: a=%d b=%d", len(got["a"]), len(got["b"]))
		}
		// NULL audit columns leave zero-values; populated ones are carried.
		if got["a"][0].CreatedBy != "" || got["a"][1].CreatedBy != "user" || got["a"][1].ThreadID != "th-1" {
			t.Errorf("NULL handling wrong: %+v", got["a"])
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet: %v", err)
		}
	})

	t.Run("empty ids → no query, empty map", func(t *testing.T) {
		r, mock := newDepMock(t)
		got, err := r.getDependencyRecordsFromTable(ctx, "dependencies", nil)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("expected empty map, got %v", got)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet (should be no queries): %v", err)
		}
	})

	t.Run("query error wrapped", func(t *testing.T) {
		r, mock := newDepMock(t)
		mock.ExpectQuery(depSelect).WillReturnError(errors.New("boom"))
		if _, err := r.getDependencyRecordsFromTable(ctx, "dependencies", []string{"a"}); err == nil ||
			!strings.Contains(err.Error(), "get dep records from dependencies") {
			t.Fatalf("expected wrapped query error, got %v", err)
		}
	})

	t.Run("scan error wrapped", func(t *testing.T) {
		r, mock := newDepMock(t)
		// Too few columns → Scan fails.
		mock.ExpectQuery(depSelect).WithArgs("a").
			WillReturnRows(sqlmock.NewRows([]string{"issue_id"}).AddRow("a"))
		if _, err := r.getDependencyRecordsFromTable(ctx, "dependencies", []string{"a"}); err == nil ||
			!strings.Contains(err.Error(), "scan") {
			t.Fatalf("expected wrapped scan error, got %v", err)
		}
	})

	t.Run("rows error wrapped", func(t *testing.T) {
		r, mock := newDepMock(t)
		cols := []string{"issue_id", "depends_on_id", "type", "created_at", "created_by", "metadata", "thread_id"}
		mock.ExpectQuery(depSelect).WithArgs("a").
			WillReturnRows(sqlmock.NewRows(cols).
				AddRow("a", "x", "blocks", nil, nil, nil, nil).
				RowError(0, errors.New("row boom")))
		if _, err := r.getDependencyRecordsFromTable(ctx, "dependencies", []string{"a"}); err == nil ||
			!strings.Contains(err.Error(), "rows") {
			t.Fatalf("expected wrapped rows error, got %v", err)
		}
	})
}

func TestGetDescendantIDs(t *testing.T) {
	ctx := context.Background()
	cteQuery := regexp.QuoteMeta("WITH RECURSIVE")

	t.Run("empty rootID → nil, no query", func(t *testing.T) {
		r, mock := newDepMock(t)
		got, err := r.getDescendantIDs(ctx, "", 0)
		if err != nil || got != nil {
			t.Fatalf("expected (nil,nil), got (%v,%v)", got, err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet (no query expected): %v", err)
		}
	})

	t.Run("returns descendants (wisps branch)", func(t *testing.T) {
		r, mock := newDepMock(t)
		mock.ExpectQuery(cteQuery).
			WillReturnRows(sqlmock.NewRows([]string{"id", "depth"}).
				AddRow("child1", 1).
				AddRow("child2", 2))
		got, err := r.getDescendantIDs(ctx, "root", 0)
		if err != nil {
			t.Fatalf("getDescendantIDs: %v", err)
		}
		if len(got) != 2 || got[0] != "child1" || got[1] != "child2" {
			t.Fatalf("unexpected descendants: %v", got)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet: %v", err)
		}
	})

	t.Run("wisps table missing → falls back to no-wisps query", func(t *testing.T) {
		r, mock := newDepMock(t)
		// First (with-wisps) query hits a 1146 table-not-exist → fallback.
		mock.ExpectQuery(cteQuery).WillReturnError(&mysql.MySQLError{Number: 1146, Message: "table wisp_dependencies doesn't exist"})
		// Second (no-wisps) query succeeds.
		mock.ExpectQuery(cteQuery).
			WillReturnRows(sqlmock.NewRows([]string{"id", "depth"}).AddRow("child1", 1))
		got, err := r.getDescendantIDs(ctx, "root", 0)
		if err != nil {
			t.Fatalf("fallback getDescendantIDs: %v", err)
		}
		if len(got) != 1 || got[0] != "child1" {
			t.Fatalf("unexpected: %v", got)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet: %v", err)
		}
	})

	t.Run("non-table-missing query error propagates", func(t *testing.T) {
		r, mock := newDepMock(t)
		mock.ExpectQuery(cteQuery).WillReturnError(errors.New("syntax boom"))
		if _, err := r.getDescendantIDs(ctx, "root", 0); err == nil || !strings.Contains(err.Error(), "syntax boom") {
			t.Fatalf("expected propagated query error, got %v", err)
		}
	})

	t.Run("scan error wrapped", func(t *testing.T) {
		r, mock := newDepMock(t)
		mock.ExpectQuery(cteQuery).
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("child1")) // missing depth col
		if _, err := r.getDescendantIDs(ctx, "root", 0); err == nil || !strings.Contains(err.Error(), "scan descendant") {
			t.Fatalf("expected scan error, got %v", err)
		}
	})

	t.Run("reachedMaxDepth → error", func(t *testing.T) {
		r, mock := newDepMock(t)
		// A row at depth == maxDepth trips the reached-max-depth guard.
		mock.ExpectQuery(cteQuery).
			WillReturnRows(sqlmock.NewRows([]string{"id", "depth"}).AddRow("child1", 2))
		if _, err := r.getDescendantIDs(ctx, "root", 2); err == nil || !strings.Contains(err.Error(), "reached max depth") {
			t.Fatalf("expected max-depth error, got %v", err)
		}
	})
}

// scanDepRow is exercised indirectly by TestGetDependencyRecordsFromTable
// (happy path + NULL columns + scan error). Add a direct guard for the raw
// scan-error surface using a one-column row.
func TestScanDepRow_ScanError(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	mock.ExpectQuery("SELECT x").WillReturnRows(sqlmock.NewRows([]string{"x"}).AddRow("only-one"))
	rows, err := db.QueryContext(context.Background(), "SELECT x")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		t.Fatal("expected a row")
	}
	if _, err := scanDepRow(rows); err == nil {
		t.Error("expected scan error from mismatched columns")
	}
}
