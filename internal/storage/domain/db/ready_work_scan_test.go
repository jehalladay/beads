package db

import (
	"errors"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
)

// TestScanStringsInto covers the shared single-column string-row scanner used by
// the descendant-id walk. Beyond the happy path it exercises the two error
// branches that were previously uncovered: a per-row Scan failure and a
// deferred rows.Err() failure surfaced after iteration.
func TestScanStringsInto(t *testing.T) {
	t.Parallel()

	t.Run("HappyPathAppendsInOrder", func(t *testing.T) {
		t.Parallel()
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock.New: %v", err)
		}
		defer db.Close()

		mock.ExpectQuery("SELECT id").WillReturnRows(
			sqlmock.NewRows([]string{"id"}).AddRow("a").AddRow("b").AddRow("c"))

		rows, err := db.Query("SELECT id FROM t")
		if err != nil {
			t.Fatalf("query: %v", err)
		}

		var out []string
		if err := scanStringsInto(rows, &out); err != nil {
			t.Fatalf("scanStringsInto: unexpected error %v", err)
		}
		if len(out) != 3 || out[0] != "a" || out[1] != "b" || out[2] != "c" {
			t.Errorf("out = %v, want [a b c]", out)
		}
	})

	t.Run("ScanErrorIsWrapped", func(t *testing.T) {
		t.Parallel()
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock.New: %v", err)
		}
		defer db.Close()

		// A row whose single column is a non-string, non-convertible value
		// makes rows.Scan(&s) fail — driving the "scan:" error branch.
		mock.ExpectQuery("SELECT id").WillReturnRows(
			sqlmock.NewRows([]string{"id"}).AddRow(nil))

		rows, err := db.Query("SELECT id FROM t")
		if err != nil {
			t.Fatalf("query: %v", err)
		}

		var out []string
		err = scanStringsInto(rows, &out)
		if err == nil {
			t.Fatal("expected a scan error, got nil")
		}
		if len(out) != 0 {
			t.Errorf("out should be empty on scan failure, got %v", out)
		}
	})

	t.Run("RowsErrIsReturned", func(t *testing.T) {
		t.Parallel()
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock.New: %v", err)
		}
		defer db.Close()

		boom := errors.New("row boom")
		// A row-level error at index 0: rows.Next() stops and rows.Err()
		// returns boom after the loop — driving the final "return rows.Err()"
		// branch (distinct from a per-row Scan failure).
		mock.ExpectQuery("SELECT id").WillReturnRows(
			sqlmock.NewRows([]string{"id"}).AddRow("ok").RowError(0, boom))

		rows, err := db.Query("SELECT id FROM t")
		if err != nil {
			t.Fatalf("query: %v", err)
		}

		var out []string
		err = scanStringsInto(rows, &out)
		if err == nil {
			t.Fatal("expected rows.Err() to surface, got nil")
		}
	})
}
