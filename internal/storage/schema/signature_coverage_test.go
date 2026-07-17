package schema

import (
	"context"
	"errors"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
)

// dolt_diff for a table returns the changed rows plus diff-metadata columns
// (from_commit/to_commit/…). dirtyTableSignature hashes the non-metadata
// columns of every row into a stable, order-independent signature.
func TestDirtyTableSignature_HashesNonMetadataColumns(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	rows := sqlmock.NewRows([]string{"id", "title", "from_commit", "to_commit"}).
		AddRow("beads-1", "hello", "aaa", "bbb").
		AddRow("beads-2", "world", "aaa", "bbb")
	mock.ExpectQuery(`SELECT \* FROM dolt_diff\('HEAD', 'WORKING', 'issues'\)`).
		WillReturnRows(rows)

	sig, err := dirtyTableSignature(context.Background(), db, "issues")
	if err != nil {
		t.Fatalf("dirtyTableSignature: %v", err)
	}
	if sig == "" {
		t.Fatal("expected a non-empty signature")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

// The signature must be independent of the row order dolt_diff happens to
// return: the same set of rows in a different order hashes identically
// (dirtyTableSignature sorts the per-row signatures before hashing).
func TestDirtyTableSignature_OrderIndependent(t *testing.T) {
	sigFor := func(t *testing.T, addRows func(*sqlmock.Rows)) string {
		t.Helper()
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock.New: %v", err)
		}
		defer db.Close()
		rows := sqlmock.NewRows([]string{"id", "val", "from_commit"})
		addRows(rows)
		mock.ExpectQuery(`SELECT \* FROM dolt_diff\('HEAD', 'WORKING', 'issues'\)`).
			WillReturnRows(rows)
		sig, err := dirtyTableSignature(context.Background(), db, "issues")
		if err != nil {
			t.Fatalf("dirtyTableSignature: %v", err)
		}
		return sig
	}

	a := sigFor(t, func(r *sqlmock.Rows) {
		r.AddRow("1", "x", "c").AddRow("2", "y", "c")
	})
	b := sigFor(t, func(r *sqlmock.Rows) {
		r.AddRow("2", "y", "c").AddRow("1", "x", "c")
	})
	if a != b {
		t.Errorf("signature is order-dependent: %q vs %q", a, b)
	}
}

// Different row content must produce a different signature.
func TestDirtyTableSignature_ContentSensitive(t *testing.T) {
	sigFor := func(t *testing.T, val string) string {
		t.Helper()
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock.New: %v", err)
		}
		defer db.Close()
		rows := sqlmock.NewRows([]string{"id", "val"}).AddRow("1", val)
		mock.ExpectQuery(`SELECT \* FROM dolt_diff\('HEAD', 'WORKING', 'issues'\)`).
			WillReturnRows(rows)
		sig, err := dirtyTableSignature(context.Background(), db, "issues")
		if err != nil {
			t.Fatalf("dirtyTableSignature: %v", err)
		}
		return sig
	}
	if sigFor(t, "before") == sigFor(t, "after") {
		t.Error("expected different content to yield different signatures")
	}
}

// A row-iteration error (surfaced by rows.Err after Next) is propagated.
func TestDirtyTableSignature_RowError(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	rows := sqlmock.NewRows([]string{"id"}).
		AddRow("1").
		RowError(0, errors.New("row iteration failed"))
	mock.ExpectQuery(`SELECT \* FROM dolt_diff\('HEAD', 'WORKING', 'issues'\)`).
		WillReturnRows(rows)

	if _, err := dirtyTableSignature(context.Background(), db, "issues"); err == nil {
		t.Fatal("expected a row-iteration error to propagate")
	}
}

func TestDirtyTableSignature_QueryError(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	mock.ExpectQuery(`SELECT \* FROM dolt_diff\('HEAD', 'WORKING', 'issues'\)`).
		WillReturnError(errors.New("boom"))

	if _, err := dirtyTableSignature(context.Background(), db, "issues"); err == nil {
		t.Fatal("expected an error from a failing dolt_diff query")
	}
}

// dirtyTableSignatures fans out over each dirty table in sorted order and
// returns a per-table signature map.
func TestDirtyTableSignatures_PerTable(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	// Sorted order: issues before wisps.
	mock.ExpectQuery(`SELECT \* FROM dolt_diff\('HEAD', 'WORKING', 'issues'\)`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("a"))
	mock.ExpectQuery(`SELECT \* FROM dolt_diff\('HEAD', 'WORKING', 'wisps'\)`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("b"))

	tables := map[string]dirtyTableState{
		"wisps":  {staged: false},
		"issues": {staged: true},
	}
	sigs, err := dirtyTableSignatures(context.Background(), db, tables)
	if err != nil {
		t.Fatalf("dirtyTableSignatures: %v", err)
	}
	if len(sigs) != 2 {
		t.Fatalf("got %d signatures, want 2", len(sigs))
	}
	if sigs["issues"] == "" || sigs["wisps"] == "" {
		t.Errorf("expected both tables to have signatures: %#v", sigs)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestDirtyTableSignatures_PropagatesError(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	mock.ExpectQuery(`SELECT \* FROM dolt_diff\('HEAD', 'WORKING', 'issues'\)`).
		WillReturnError(errors.New("boom"))

	_, err = dirtyTableSignatures(context.Background(), db,
		map[string]dirtyTableState{"issues": {}})
	if err == nil {
		t.Fatal("expected the underlying signature error to propagate")
	}
}

// changedDirtyTableSignatures re-reads each table's current signature and
// reports the ones whose signature differs from the captured "before" map.
func TestChangedDirtyTableSignatures_DetectsChange(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	// First compute a baseline signature for "issues".
	mock.ExpectQuery(`SELECT \* FROM dolt_diff\('HEAD', 'WORKING', 'issues'\)`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "val"}).AddRow("1", "old"))
	before, err := dirtyTableSignatures(context.Background(), db,
		map[string]dirtyTableState{"issues": {}})
	if err != nil {
		t.Fatalf("baseline: %v", err)
	}

	// Now the same table has different content -> reported as changed.
	mock.ExpectQuery(`SELECT \* FROM dolt_diff\('HEAD', 'WORKING', 'issues'\)`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "val"}).AddRow("1", "new"))
	changed, err := changedDirtyTableSignatures(context.Background(), db, before)
	if err != nil {
		t.Fatalf("changedDirtyTableSignatures: %v", err)
	}
	if len(changed) != 1 || changed[0] != "issues" {
		t.Errorf("expected [issues] changed, got %v", changed)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestChangedDirtyTableSignatures_NoChange(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	mock.ExpectQuery(`SELECT \* FROM dolt_diff\('HEAD', 'WORKING', 'issues'\)`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("1"))
	before, err := dirtyTableSignatures(context.Background(), db,
		map[string]dirtyTableState{"issues": {}})
	if err != nil {
		t.Fatalf("baseline: %v", err)
	}

	mock.ExpectQuery(`SELECT \* FROM dolt_diff\('HEAD', 'WORKING', 'issues'\)`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("1"))
	changed, err := changedDirtyTableSignatures(context.Background(), db, before)
	if err != nil {
		t.Fatalf("changedDirtyTableSignatures: %v", err)
	}
	if len(changed) != 0 {
		t.Errorf("expected no changes, got %v", changed)
	}
}

func TestChangedDirtyTableSignatures_PropagatesError(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	mock.ExpectQuery(`SELECT \* FROM dolt_diff\('HEAD', 'WORKING', 'issues'\)`).
		WillReturnError(errors.New("boom"))

	_, err = changedDirtyTableSignatures(context.Background(), db,
		map[string]string{"issues": "deadbeef"})
	if err == nil {
		t.Fatal("expected the underlying signature error to propagate")
	}
}

// CurrentIgnoredVersion reads MAX(version) from the ignored-source cursor table.
func TestCurrentIgnoredVersion_ReadsIgnoredCursor(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	mock.ExpectQuery(`SELECT COALESCE\(MAX\(version\), 0\) FROM ignored_schema_migrations`).
		WillReturnRows(sqlmock.NewRows([]string{"version"}).AddRow(7))

	v, err := CurrentIgnoredVersion(context.Background(), db)
	if err != nil {
		t.Fatalf("CurrentIgnoredVersion: %v", err)
	}
	if v != 7 {
		t.Errorf("got version %d, want 7", v)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

// PendingIgnoredVersions returns the ignored-source migrations newer than the
// recorded cursor version. With the cursor at a version at/above the latest,
// nothing is pending.
func TestPendingIgnoredVersions_NoneWhenAtLatest(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	latest := LatestIgnoredVersion()
	mock.ExpectQuery(`SELECT COALESCE\(MAX\(version\), 0\) FROM ignored_schema_migrations`).
		WillReturnRows(sqlmock.NewRows([]string{"version"}).AddRow(latest))

	pending, err := PendingIgnoredVersions(context.Background(), db)
	if err != nil {
		t.Fatalf("PendingIgnoredVersions: %v", err)
	}
	if len(pending) != 0 {
		t.Errorf("expected no pending ignored versions at latest, got %v", pending)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

// A fresh DB (cursor at 0) has every ignored-source migration pending.
func TestPendingIgnoredVersions_AllWhenFresh(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	mock.ExpectQuery(`SELECT COALESCE\(MAX\(version\), 0\) FROM ignored_schema_migrations`).
		WillReturnRows(sqlmock.NewRows([]string{"version"}).AddRow(0))

	pending, err := PendingIgnoredVersions(context.Background(), db)
	if err != nil {
		t.Fatalf("PendingIgnoredVersions: %v", err)
	}
	if latest := LatestIgnoredVersion(); latest > 0 && len(pending) == 0 {
		t.Errorf("expected pending ignored versions on a fresh DB (latest=%d), got none", latest)
	}
}

// atLatest reports true once the recorded version reaches the source's latest,
// and false when the version-read query fails.
func TestMigrationSourceAtLatest(t *testing.T) {
	t.Run("true when at latest", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock.New: %v", err)
		}
		defer db.Close()
		mock.ExpectQuery(`SELECT COALESCE\(MAX\(version\), 0\) FROM schema_migrations`).
			WillReturnRows(sqlmock.NewRows([]string{"version"}).AddRow(mainSource.latest()))
		if !mainSource.atLatest(context.Background(), db) {
			t.Error("expected atLatest true when version == latest")
		}
	})

	t.Run("false on query error", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock.New: %v", err)
		}
		defer db.Close()
		mock.ExpectQuery(`SELECT COALESCE\(MAX\(version\), 0\) FROM schema_migrations`).
			WillReturnError(errors.New("boom"))
		if mainSource.atLatest(context.Background(), db) {
			t.Error("expected atLatest false when the version read errors")
		}
	})
}

// currentVersion treats a missing cursor table (error 1146) as version 0
// rather than surfacing an error.
func TestMigrationSourceCurrentVersion_MissingTableIsZero(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	mock.ExpectQuery(`SELECT COALESCE\(MAX\(version\), 0\) FROM schema_migrations`).
		WillReturnError(errors.New("Error 1146 (42S02): Table 'x.schema_migrations' doesn't exist"))

	v, err := mainSource.currentVersion(context.Background(), db)
	if err != nil {
		t.Fatalf("currentVersion: %v", err)
	}
	if v != 0 {
		t.Errorf("got version %d, want 0 for a missing cursor table", v)
	}
}

// A non-table-missing query error is wrapped and surfaced.
func TestMigrationSourceCurrentVersion_QueryErrorWrapped(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	mock.ExpectQuery(`SELECT COALESCE\(MAX\(version\), 0\) FROM schema_migrations`).
		WillReturnError(errors.New("connection reset"))

	if _, err := mainSource.currentVersion(context.Background(), db); err == nil {
		t.Fatal("expected a wrapped error for a non-table-missing failure")
	}
}
