package schema

import (
	"context"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/go-sql-driver/mysql"
)

// A foreign or pre-migrations Dolt DB may have no schema_migrations table at
// all. The drift checks must then behave like currentVersion (treat as v0),
// yielding the intended TYPED drift error rather than a raw "Error 1146: table
// not found" that defeats IsSchemaSkewError/IsSchemaBehindError and the
// BD_IGNORE_SCHEMA_SKEW escape hatch (bd-xmyw, GH#3231).

func tableNotExistErr() *mysql.MySQLError {
	return &mysql.MySQLError{Number: 1146, Message: "table not found: schema_migrations"}
}

func TestCheckSchemaSkew_TableNotExist_TreatedAsFresh(t *testing.T) {
	t.Setenv("BD_IGNORE_SCHEMA_SKEW", "")
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	mock.ExpectQuery(`SELECT COALESCE\(MAX\(version\), 0\) FROM schema_migrations`).
		WillReturnError(tableNotExistErr())

	// No schema_migrations => v0 => not ahead of the binary => no forward skew.
	if err := checkSchemaSkew(context.Background(), db); err != nil {
		t.Fatalf("checkSchemaSkew(missing table) = %v, want nil (treat as fresh v0)", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
}

func TestCheckBehindDrift_TableNotExist_ReturnsTypedBehindError(t *testing.T) {
	t.Setenv("BD_IGNORE_SCHEMA_SKEW", "")
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	mock.ExpectQuery(`SELECT COALESCE\(MAX\(version\), 0\) FROM schema_migrations`).
		WillReturnError(tableNotExistErr())

	err = CheckBehindDrift(context.Background(), db)
	if err == nil {
		t.Fatal("CheckBehindDrift(missing table) = nil, want a behind-drift error")
	}
	// Must be the TYPED error (v0 < LatestVersion), not a raw "Error 1146".
	if !IsSchemaBehindError(err) {
		t.Fatalf("CheckBehindDrift(missing table) = %v, want *SchemaBehindError (IsSchemaBehindError=true)", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
}

func TestCheckBehindDrift_TableNotExist_IgnoreSkewDowngrades(t *testing.T) {
	// With the escape hatch set, the missing-table case downgrades to a warning
	// and returns nil — the branch that was previously unreachable because the
	// raw table-not-found error short-circuited before it.
	t.Setenv("BD_IGNORE_SCHEMA_SKEW", "1")
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	mock.ExpectQuery(`SELECT COALESCE\(MAX\(version\), 0\) FROM schema_migrations`).
		WillReturnError(tableNotExistErr())

	if err := CheckBehindDrift(context.Background(), db); err != nil {
		t.Fatalf("CheckBehindDrift(missing table, ignore=1) = %v, want nil (downgraded to warning)", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
}

// A non-table-not-exist scan error must still propagate (not be swallowed as v0).
func TestSchemaMigrationsVersion_OtherErrorPropagates(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	mock.ExpectQuery(`SELECT COALESCE\(MAX\(version\), 0\) FROM schema_migrations`).
		WillReturnError(&mysql.MySQLError{Number: 1045, Message: "access denied"})

	if _, err := schemaMigrationsVersion(context.Background(), db); err == nil {
		t.Fatal("schemaMigrationsVersion(access-denied) = nil error, want propagated error")
	}
}
