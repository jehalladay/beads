package doltserver

import (
	"database/sql"
	"errors"
	"regexp"
	"strings"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
)

// withMockOpenDB swaps the injectable openDB seam for one returning the given
// sqlmock DB, and restores the original in t.Cleanup. The returned mock has
// ping monitoring enabled so tests can control PingContext behavior.
func withMockOpenDB(t *testing.T) sqlmock.Sqlmock {
	t.Helper()
	db, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	orig := openDB
	openDB = func(string) (*sql.DB, error) { return db, nil }
	t.Cleanup(func() {
		openDB = orig
		_ = db.Close()
	})
	return mock
}

func TestEnsureGlobalDatabase_OpenError(t *testing.T) {
	orig := openDB
	openDB = func(string) (*sql.DB, error) { return nil, errors.New("boom") }
	t.Cleanup(func() { openDB = orig })

	err := EnsureGlobalDatabase("h", 1, "u", "p")
	if err == nil || !strings.Contains(err.Error(), "failed to open connection") {
		t.Fatalf("want open-connection error, got %v", err)
	}
}

func TestEnsureGlobalDatabase_PingError(t *testing.T) {
	mock := withMockOpenDB(t)
	mock.ExpectPing().WillReturnError(errors.New("unreachable"))

	err := EnsureGlobalDatabase("h", 1, "u", "p")
	if err == nil || !strings.Contains(err.Error(), "server not reachable") {
		t.Fatalf("want server-not-reachable error, got %v", err)
	}
}

func TestEnsureGlobalDatabase_CreateSuccess(t *testing.T) {
	mock := withMockOpenDB(t)
	mock.ExpectPing()
	mock.ExpectExec("CREATE DATABASE IF NOT EXISTS").WillReturnResult(sqlmock.NewResult(0, 0))

	if err := EnsureGlobalDatabase("h", 1, "u", "p"); err != nil {
		t.Fatalf("want nil, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestEnsureGlobalDatabase_ExistsIsBenign(t *testing.T) {
	// A "database exists" / 1007 error is idempotent and must be swallowed.
	for _, msg := range []string{"Error 1007: database exists", "DATABASE EXISTS foo"} {
		mock := withMockOpenDB(t)
		mock.ExpectPing()
		mock.ExpectExec("CREATE DATABASE IF NOT EXISTS").WillReturnError(errors.New(msg))
		if err := EnsureGlobalDatabase("h", 1, "u", "p"); err != nil {
			t.Fatalf("msg=%q: want benign nil, got %v", msg, err)
		}
	}
}

func TestEnsureGlobalDatabase_CreateErrorWrapped(t *testing.T) {
	mock := withMockOpenDB(t)
	mock.ExpectPing()
	mock.ExpectExec("CREATE DATABASE IF NOT EXISTS").WillReturnError(errors.New("permission denied"))

	err := EnsureGlobalDatabase("h", 1, "u", "p")
	if err == nil || !strings.Contains(err.Error(), "failed to create") {
		t.Fatalf("want failed-to-create error, got %v", err)
	}
}

func TestFlushWorkingSet_OpenError(t *testing.T) {
	orig := openDB
	openDB = func(string) (*sql.DB, error) { return nil, errors.New("boom") }
	t.Cleanup(func() { openDB = orig })

	err := FlushWorkingSet("h", 1)
	if err == nil || !strings.Contains(err.Error(), "failed to open connection") {
		t.Fatalf("want open-connection error, got %v", err)
	}
}

func TestFlushWorkingSet_PingError(t *testing.T) {
	mock := withMockOpenDB(t)
	mock.ExpectPing().WillReturnError(errors.New("down"))

	err := FlushWorkingSet("h", 1)
	if err == nil || !strings.Contains(err.Error(), "server not reachable") {
		t.Fatalf("want server-not-reachable error, got %v", err)
	}
}

func TestFlushWorkingSet_ShowDatabasesError(t *testing.T) {
	mock := withMockOpenDB(t)
	mock.ExpectPing()
	mock.ExpectQuery("SHOW DATABASES").WillReturnError(errors.New("query fail"))

	err := FlushWorkingSet("h", 1)
	if err == nil || !strings.Contains(err.Error(), "failed to list databases") {
		t.Fatalf("want list-databases error, got %v", err)
	}
}

func TestFlushWorkingSet_RowsErrFailsLoud(t *testing.T) {
	mock := withMockOpenDB(t)
	mock.ExpectPing()
	rows := sqlmock.NewRows([]string{"Database"}).AddRow("beads_global").RowError(0, errors.New("truncated"))
	mock.ExpectQuery("SHOW DATABASES").WillReturnRows(rows)

	err := FlushWorkingSet("h", 1)
	if err == nil || !strings.Contains(err.Error(), "failed to read database list") {
		t.Fatalf("want read-database-list error, got %v", err)
	}
}

func TestFlushWorkingSet_OnlySystemDatabases(t *testing.T) {
	// information_schema/mysql/performance_schema are filtered out → empty
	// working list → early nil return (no per-db dolt_status queries).
	mock := withMockOpenDB(t)
	mock.ExpectPing()
	rows := sqlmock.NewRows([]string{"Database"}).
		AddRow("information_schema").
		AddRow("mysql").
		AddRow("performance_schema")
	mock.ExpectQuery("SHOW DATABASES").WillReturnRows(rows)

	if err := FlushWorkingSet("h", 1); err != nil {
		t.Fatalf("want nil, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestFlushWorkingSet_StatusMissingSkips(t *testing.T) {
	// A database whose dolt_status query errors (e.g. non-beads db without the
	// system table) is skipped, not fatal.
	mock := withMockOpenDB(t)
	mock.ExpectPing()
	mock.ExpectQuery("SHOW DATABASES").WillReturnRows(
		sqlmock.NewRows([]string{"Database"}).AddRow("randomdb"))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) > 0 FROM `randomdb`.dolt_status")).
		WillReturnError(errors.New("no such table"))

	if err := FlushWorkingSet("h", 1); err != nil {
		t.Fatalf("want nil, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestFlushWorkingSet_NoChangesSkips(t *testing.T) {
	mock := withMockOpenDB(t)
	mock.ExpectPing()
	mock.ExpectQuery("SHOW DATABASES").WillReturnRows(
		sqlmock.NewRows([]string{"Database"}).AddRow("beads_global"))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) > 0 FROM `beads_global`.dolt_status")).
		WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(false))

	if err := FlushWorkingSet("h", 1); err != nil {
		t.Fatalf("want nil, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestFlushWorkingSet_CommitSuccess(t *testing.T) {
	mock := withMockOpenDB(t)
	mock.ExpectPing()
	mock.ExpectQuery("SHOW DATABASES").WillReturnRows(
		sqlmock.NewRows([]string{"Database"}).AddRow("beads_global"))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) > 0 FROM `beads_global`.dolt_status")).
		WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(true))
	mock.ExpectExec(regexp.QuoteMeta("USE `beads_global`")).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("DOLT_COMMIT").WillReturnResult(sqlmock.NewResult(0, 1))

	if err := FlushWorkingSet("h", 1); err != nil {
		t.Fatalf("want nil, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestFlushWorkingSet_UseErrorSkips(t *testing.T) {
	mock := withMockOpenDB(t)
	mock.ExpectPing()
	mock.ExpectQuery("SHOW DATABASES").WillReturnRows(
		sqlmock.NewRows([]string{"Database"}).AddRow("beads_global"))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) > 0 FROM `beads_global`.dolt_status")).
		WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(true))
	mock.ExpectExec(regexp.QuoteMeta("USE `beads_global`")).WillReturnError(errors.New("use failed"))

	if err := FlushWorkingSet("h", 1); err != nil {
		t.Fatalf("want nil (skip on USE error), got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestFlushWorkingSet_NothingToCommitBenign(t *testing.T) {
	// DOLT_COMMIT returning "nothing to commit" is a benign skip, not an error.
	mock := withMockOpenDB(t)
	mock.ExpectPing()
	mock.ExpectQuery("SHOW DATABASES").WillReturnRows(
		sqlmock.NewRows([]string{"Database"}).AddRow("beads_global"))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) > 0 FROM `beads_global`.dolt_status")).
		WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(true))
	mock.ExpectExec(regexp.QuoteMeta("USE `beads_global`")).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("DOLT_COMMIT").WillReturnError(errors.New("nothing to commit"))

	if err := FlushWorkingSet("h", 1); err != nil {
		t.Fatalf("want nil (benign nothing-to-commit), got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestFlushWorkingSet_CommitErrorSkips(t *testing.T) {
	// A real DOLT_COMMIT error is logged and skipped (best-effort), not fatal.
	mock := withMockOpenDB(t)
	mock.ExpectPing()
	mock.ExpectQuery("SHOW DATABASES").WillReturnRows(
		sqlmock.NewRows([]string{"Database"}).AddRow("beads_global"))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) > 0 FROM `beads_global`.dolt_status")).
		WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(true))
	mock.ExpectExec(regexp.QuoteMeta("USE `beads_global`")).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("DOLT_COMMIT").WillReturnError(errors.New("disk full"))

	if err := FlushWorkingSet("h", 1); err != nil {
		t.Fatalf("want nil (skip on commit error), got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}
