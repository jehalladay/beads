package schema

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	mysql "github.com/go-sql-driver/mysql"
)

// These tests cover the ignored-source version accessors and the currentVersion
// error branches with sqlmock (no live dolt), mirroring content_hash_test.go.

func TestCurrentIgnoredVersion_ReadsMaxFromIgnoredCursorTable(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	mock.ExpectQuery(`SELECT COALESCE\(MAX\(version\), 0\) FROM ignored_schema_migrations`).
		WillReturnRows(sqlmock.NewRows([]string{"v"}).AddRow(7))

	got, err := CurrentIgnoredVersion(context.Background(), db)
	if err != nil {
		t.Fatalf("CurrentIgnoredVersion: %v", err)
	}
	if got != 7 {
		t.Errorf("CurrentIgnoredVersion = %d, want 7", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestCurrentVersion_TableNotExistReturnsZero(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	// A 1146 (table doesn't exist) error must be swallowed → (0, nil): a brand
	// new database has no cursor table yet.
	mock.ExpectQuery(`SELECT COALESCE\(MAX\(version\), 0\) FROM ignored_schema_migrations`).
		WillReturnError(&mysql.MySQLError{Number: 1146, Message: "Table 'ignored_schema_migrations' doesn't exist"})

	got, err := CurrentIgnoredVersion(context.Background(), db)
	if err != nil {
		t.Fatalf("CurrentIgnoredVersion(table-not-exist) err = %v, want nil", err)
	}
	if got != 0 {
		t.Errorf("CurrentIgnoredVersion(table-not-exist) = %d, want 0", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestCurrentVersion_GenericErrorPropagates(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	// A non-table-missing error must be wrapped and returned.
	mock.ExpectQuery(`SELECT COALESCE\(MAX\(version\), 0\) FROM ignored_schema_migrations`).
		WillReturnError(errors.New("connection reset"))

	if _, err := CurrentIgnoredVersion(context.Background(), db); err == nil {
		t.Fatal("CurrentIgnoredVersion(generic error) err = nil, want propagated error")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestPendingIgnoredVersions_CurrentVersionErrorPropagates(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	// pendingVersions calls currentVersion first; a generic error there must
	// bubble up rather than being treated as version 0.
	mock.ExpectQuery(`SELECT COALESCE\(MAX\(version\), 0\) FROM ignored_schema_migrations`).
		WillReturnError(errors.New("connection reset"))

	if _, err := PendingIgnoredVersions(context.Background(), db); err == nil {
		t.Fatal("PendingIgnoredVersions err = nil, want propagated currentVersion error")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestPendingIgnoredVersions_ComputesPendingAboveCurrent(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	// With the ignored cursor at 0, every embedded ignored migration (if any)
	// is pending; the result must be sorted and strictly greater than current.
	mock.ExpectQuery(`SELECT COALESCE\(MAX\(version\), 0\) FROM ignored_schema_migrations`).
		WillReturnRows(sqlmock.NewRows([]string{"v"}).AddRow(0))

	pending, err := PendingIgnoredVersions(context.Background(), db)
	if err != nil {
		t.Fatalf("PendingIgnoredVersions: %v", err)
	}
	for i, v := range pending {
		if v <= 0 {
			t.Errorf("pending[%d] = %d, want > current (0)", i, v)
		}
		if i > 0 && v <= pending[i-1] {
			t.Errorf("pending not strictly increasing at %d: %v", i, pending)
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestParseVersion(t *testing.T) {
	if v, err := parseVersion("0042_add_something.up.sql"); err != nil || v != 42 {
		t.Errorf("parseVersion(valid) = %d,%v, want 42,nil", v, err)
	}
	// Non-numeric prefix → strconv.Atoi error.
	if _, err := parseVersion("abc_not_numeric.up.sql"); err == nil {
		t.Error("parseVersion(non-numeric) err = nil, want Atoi error")
	}
}
