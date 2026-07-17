package versioncontrolops

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

// newConflictMock returns a mock *sql.DB (which satisfies DBConn) plus its
// controller. QueryMatcherRegexp (sqlmock's default) matches the expected
// substrings against the multi-line conflict-resolution SQL.
func newConflictMock(t *testing.T) (*sql.DB, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, mock
}

func ns(s string) sql.NullString { return sql.NullString{String: s, Valid: true} }

// --- resolveConflictDepTarget (pure) -----------------------------------------

func TestResolveConflictDepTarget(t *testing.T) {
	t.Parallel()
	null := sql.NullString{}

	if got, ok := resolveConflictDepTarget(ns("bd-1"), ns("w-9"), ns("ext")); !ok || got != "bd-1" {
		t.Errorf("issue precedence: got %q,%v want bd-1,true", got, ok)
	}
	if got, ok := resolveConflictDepTarget(null, ns("w-9"), ns("ext")); !ok || got != "w-9" {
		t.Errorf("wisp precedence: got %q,%v want w-9,true", got, ok)
	}
	if got, ok := resolveConflictDepTarget(null, null, ns("external:x")); !ok || got != "external:x" {
		t.Errorf("external precedence: got %q,%v want external:x,true", got, ok)
	}
	if got, ok := resolveConflictDepTarget(null, null, null); ok || got != "" {
		t.Errorf("all-null: got %q,%v want \"\",false", got, ok)
	}
}

// --- dependencyConflictsAreAuditOnly -----------------------------------------

func TestDependencyConflictsAreAuditOnly(t *testing.T) {
	t.Parallel()
	q := `FROM dolt_conflicts_dependencies`
	cols := []string{
		"our_id", "their_id", "our_issue_id", "their_issue_id",
		"our_depends_on_issue_id", "their_depends_on_issue_id",
		"our_depends_on_wisp_id", "their_depends_on_wisp_id",
		"our_depends_on_external", "their_depends_on_external",
		"our_type", "their_type",
	}
	// A row where both sides carry identical natural identity + type.
	auditRow := func() *sqlmock.Rows {
		return sqlmock.NewRows(cols).AddRow(
			"d1", "d1", "bd-1", "bd-1", "bd-2", "bd-2",
			nil, nil, nil, nil, "blocks", "blocks")
	}

	t.Run("identical edge is audit-only", func(t *testing.T) {
		db, mock := newConflictMock(t)
		mock.ExpectQuery(q).WillReturnRows(auditRow())
		ok, err := dependencyConflictsAreAuditOnly(context.Background(), db)
		if err != nil || !ok {
			t.Fatalf("got ok=%v err=%v, want true,nil", ok, err)
		}
	})

	t.Run("empty conflicts is audit-only", func(t *testing.T) {
		db, mock := newConflictMock(t)
		mock.ExpectQuery(q).WillReturnRows(sqlmock.NewRows(cols))
		ok, err := dependencyConflictsAreAuditOnly(context.Background(), db)
		if err != nil || !ok {
			t.Fatalf("got ok=%v err=%v, want true,nil", ok, err)
		}
	})

	t.Run("add/delete conflict is not audit-only", func(t *testing.T) {
		db, mock := newConflictMock(t)
		mock.ExpectQuery(q).WillReturnRows(sqlmock.NewRows(cols).AddRow(
			"d1", nil, "bd-1", nil, "bd-2", nil,
			nil, nil, nil, nil, "blocks", nil))
		ok, err := dependencyConflictsAreAuditOnly(context.Background(), db)
		if err != nil || ok {
			t.Fatalf("got ok=%v err=%v, want false,nil", ok, err)
		}
	})

	t.Run("differing issue_id is not audit-only", func(t *testing.T) {
		db, mock := newConflictMock(t)
		mock.ExpectQuery(q).WillReturnRows(sqlmock.NewRows(cols).AddRow(
			"d1", "d1", "bd-1", "bd-9", "bd-2", "bd-2",
			nil, nil, nil, nil, "blocks", "blocks"))
		ok, err := dependencyConflictsAreAuditOnly(context.Background(), db)
		if err != nil || ok {
			t.Fatalf("got ok=%v err=%v, want false,nil", ok, err)
		}
	})

	t.Run("differing target is not audit-only", func(t *testing.T) {
		db, mock := newConflictMock(t)
		mock.ExpectQuery(q).WillReturnRows(sqlmock.NewRows(cols).AddRow(
			"d1", "d1", "bd-1", "bd-1", "bd-2", "bd-3",
			nil, nil, nil, nil, "blocks", "blocks"))
		ok, err := dependencyConflictsAreAuditOnly(context.Background(), db)
		if err != nil || ok {
			t.Fatalf("got ok=%v err=%v, want false,nil", ok, err)
		}
	})

	t.Run("differing type is not audit-only", func(t *testing.T) {
		db, mock := newConflictMock(t)
		mock.ExpectQuery(q).WillReturnRows(sqlmock.NewRows(cols).AddRow(
			"d1", "d1", "bd-1", "bd-1", "bd-2", "bd-2",
			nil, nil, nil, nil, "blocks", "parent-child"))
		ok, err := dependencyConflictsAreAuditOnly(context.Background(), db)
		if err != nil || ok {
			t.Fatalf("got ok=%v err=%v, want false,nil", ok, err)
		}
	})

	t.Run("query error propagates", func(t *testing.T) {
		db, mock := newConflictMock(t)
		mock.ExpectQuery(q).WillReturnError(errors.New("boom"))
		if _, err := dependencyConflictsAreAuditOnly(context.Background(), db); err == nil {
			t.Fatal("expected query error")
		}
	})

	t.Run("scan error propagates", func(t *testing.T) {
		db, mock := newConflictMock(t)
		// Too few columns for the 12-target Scan.
		mock.ExpectQuery(q).WillReturnRows(sqlmock.NewRows([]string{"our_id"}).AddRow("d1"))
		if _, err := dependencyConflictsAreAuditOnly(context.Background(), db); err == nil {
			t.Fatal("expected scan error")
		}
	})
}

// --- configConflictsAreMemoryConvergent & resolvedConfigConflictKeys ---------

func TestConfigConflictsAreMemoryConvergent(t *testing.T) {
	t.Parallel()
	q := `FROM dolt_conflicts_config`
	cols := []string{"our_key", "their_key"}

	t.Run("all memory keys are convergent", func(t *testing.T) {
		db, mock := newConflictMock(t)
		mock.ExpectQuery(q).WillReturnRows(sqlmock.NewRows(cols).
			AddRow("kv.memory.a", "kv.memory.a").
			AddRow("kv.memory.b", "kv.memory.b"))
		ok, err := configConflictsAreMemoryConvergent(context.Background(), db)
		if err != nil || !ok {
			t.Fatalf("got ok=%v err=%v, want true,nil", ok, err)
		}
	})

	t.Run("non-memory key is not convergent", func(t *testing.T) {
		db, mock := newConflictMock(t)
		mock.ExpectQuery(q).WillReturnRows(sqlmock.NewRows(cols).
			AddRow("issue_prefix", "issue_prefix"))
		ok, err := configConflictsAreMemoryConvergent(context.Background(), db)
		if err != nil || ok {
			t.Fatalf("got ok=%v err=%v, want false,nil", ok, err)
		}
	})

	t.Run("add/delete of a memory key still convergent", func(t *testing.T) {
		db, mock := newConflictMock(t)
		mock.ExpectQuery(q).WillReturnRows(sqlmock.NewRows(cols).
			AddRow("kv.memory.a", nil))
		ok, err := configConflictsAreMemoryConvergent(context.Background(), db)
		if err != nil || !ok {
			t.Fatalf("got ok=%v err=%v, want true,nil", ok, err)
		}
	})

	t.Run("query error propagates", func(t *testing.T) {
		db, mock := newConflictMock(t)
		mock.ExpectQuery(q).WillReturnError(errors.New("boom"))
		if _, err := configConflictsAreMemoryConvergent(context.Background(), db); err == nil {
			t.Fatal("expected query error")
		}
	})

	t.Run("scan error propagates", func(t *testing.T) {
		db, mock := newConflictMock(t)
		mock.ExpectQuery(q).WillReturnRows(sqlmock.NewRows([]string{"our_key"}).AddRow("kv.memory.a"))
		if _, err := configConflictsAreMemoryConvergent(context.Background(), db); err == nil {
			t.Fatal("expected scan error")
		}
	})
}

func TestResolvedConfigConflictKeys(t *testing.T) {
	t.Parallel()
	q := regexp.QuoteMeta("SELECT COALESCE(our_key, their_key) FROM dolt_conflicts_config")

	t.Run("returns coalesced keys", func(t *testing.T) {
		db, mock := newConflictMock(t)
		mock.ExpectQuery(q).WillReturnRows(sqlmock.NewRows([]string{"k"}).
			AddRow("kv.memory.a").
			AddRow("kv.memory.b").
			AddRow(nil)) // NULL row is skipped
		keys, err := resolvedConfigConflictKeys(context.Background(), db)
		if err != nil {
			t.Fatalf("resolvedConfigConflictKeys: %v", err)
		}
		if len(keys) != 2 || keys[0] != "kv.memory.a" || keys[1] != "kv.memory.b" {
			t.Fatalf("keys = %v, want [kv.memory.a kv.memory.b]", keys)
		}
	})

	t.Run("query error propagates", func(t *testing.T) {
		db, mock := newConflictMock(t)
		mock.ExpectQuery(q).WillReturnError(errors.New("boom"))
		if _, err := resolvedConfigConflictKeys(context.Background(), db); err == nil {
			t.Fatal("expected query error")
		}
	})

	t.Run("scan error propagates", func(t *testing.T) {
		db, mock := newConflictMock(t)
		mock.ExpectQuery(q).WillReturnRows(
			sqlmock.NewRows([]string{"a", "b"}).AddRow("x", "y"))
		if _, err := resolvedConfigConflictKeys(context.Background(), db); err == nil {
			t.Fatal("expected scan error")
		}
	})
}

// --- schemaMigrationsConflictsAreVintageOnly ---------------------------------

func TestSchemaMigrationsConflictsAreVintageOnly(t *testing.T) {
	t.Parallel()
	q := `FROM dolt_conflicts_schema_migrations`
	cols := []string{"our_version", "their_version", "our_content_hash", "their_content_hash"}

	t.Run("equal hashes same version is vintage-only", func(t *testing.T) {
		db, mock := newConflictMock(t)
		mock.ExpectQuery(q).WillReturnRows(sqlmock.NewRows(cols).AddRow(53, 53, "h", "h"))
		ok, err := schemaMigrationsConflictsAreVintageOnly(context.Background(), db)
		if err != nil || !ok {
			t.Fatalf("got ok=%v err=%v, want true,nil", ok, err)
		}
	})

	t.Run("one-sided NULL hash is vintage-only", func(t *testing.T) {
		db, mock := newConflictMock(t)
		mock.ExpectQuery(q).WillReturnRows(sqlmock.NewRows(cols).AddRow(53, 53, nil, "h"))
		ok, err := schemaMigrationsConflictsAreVintageOnly(context.Background(), db)
		if err != nil || !ok {
			t.Fatalf("got ok=%v err=%v, want true,nil", ok, err)
		}
	})

	t.Run("differing versions not vintage", func(t *testing.T) {
		db, mock := newConflictMock(t)
		mock.ExpectQuery(q).WillReturnRows(sqlmock.NewRows(cols).AddRow(53, 54, "h", "h"))
		ok, err := schemaMigrationsConflictsAreVintageOnly(context.Background(), db)
		if err != nil || ok {
			t.Fatalf("got ok=%v err=%v, want false,nil", ok, err)
		}
	})

	t.Run("two different non-empty hashes not vintage", func(t *testing.T) {
		db, mock := newConflictMock(t)
		mock.ExpectQuery(q).WillReturnRows(sqlmock.NewRows(cols).AddRow(53, 53, "h1", "h2"))
		ok, err := schemaMigrationsConflictsAreVintageOnly(context.Background(), db)
		if err != nil || ok {
			t.Fatalf("got ok=%v err=%v, want false,nil", ok, err)
		}
	})

	t.Run("query error propagates", func(t *testing.T) {
		db, mock := newConflictMock(t)
		mock.ExpectQuery(q).WillReturnError(errors.New("boom"))
		if _, err := schemaMigrationsConflictsAreVintageOnly(context.Background(), db); err == nil {
			t.Fatal("expected query error")
		}
	})

	t.Run("scan error propagates", func(t *testing.T) {
		db, mock := newConflictMock(t)
		mock.ExpectQuery(q).WillReturnRows(sqlmock.NewRows([]string{"v"}).AddRow(53))
		if _, err := schemaMigrationsConflictsAreVintageOnly(context.Background(), db); err == nil {
			t.Fatal("expected scan error")
		}
	})
}

// --- resolveSchemaMigrationsVintageConflicts ---------------------------------

func TestResolveSchemaMigrationsVintageConflicts(t *testing.T) {
	t.Parallel()
	selQ := `FROM dolt_conflicts_schema_migrations`

	t.Run("backfills missing hash then resolves ours", func(t *testing.T) {
		db, mock := newConflictMock(t)
		mock.ExpectQuery(selQ).WillReturnRows(
			sqlmock.NewRows([]string{"our_version", "our_content_hash", "their_content_hash"}).
				AddRow(53, "", "theirhash").
				AddRow(52, "keep", "keep")) // ours present -> no backfill
		mock.ExpectExec(regexp.QuoteMeta("UPDATE schema_migrations SET content_hash = ? WHERE version = ?")).
			WithArgs("theirhash", int64(53)).
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectExec(regexp.QuoteMeta("CALL DOLT_CONFLICTS_RESOLVE('--ours', 'schema_migrations')")).
			WillReturnResult(sqlmock.NewResult(0, 1))
		if err := resolveSchemaMigrationsVintageConflicts(context.Background(), db); err != nil {
			t.Fatalf("resolveSchemaMigrationsVintageConflicts: %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet expectations: %v", err)
		}
	})

	t.Run("no backfill needed still resolves", func(t *testing.T) {
		db, mock := newConflictMock(t)
		mock.ExpectQuery(selQ).WillReturnRows(
			sqlmock.NewRows([]string{"our_version", "our_content_hash", "their_content_hash"}).
				AddRow(53, "have", "have"))
		mock.ExpectExec(regexp.QuoteMeta("CALL DOLT_CONFLICTS_RESOLVE('--ours', 'schema_migrations')")).
			WillReturnResult(sqlmock.NewResult(0, 1))
		if err := resolveSchemaMigrationsVintageConflicts(context.Background(), db); err != nil {
			t.Fatalf("resolveSchemaMigrationsVintageConflicts: %v", err)
		}
	})

	t.Run("query error propagates", func(t *testing.T) {
		db, mock := newConflictMock(t)
		mock.ExpectQuery(selQ).WillReturnError(errors.New("boom"))
		if err := resolveSchemaMigrationsVintageConflicts(context.Background(), db); err == nil {
			t.Fatal("expected query error")
		}
	})

	t.Run("backfill exec error propagates", func(t *testing.T) {
		db, mock := newConflictMock(t)
		mock.ExpectQuery(selQ).WillReturnRows(
			sqlmock.NewRows([]string{"our_version", "our_content_hash", "their_content_hash"}).
				AddRow(53, "", "theirhash"))
		mock.ExpectExec(regexp.QuoteMeta("UPDATE schema_migrations SET content_hash = ? WHERE version = ?")).
			WillReturnError(errors.New("boom"))
		if err := resolveSchemaMigrationsVintageConflicts(context.Background(), db); err == nil {
			t.Fatal("expected backfill error")
		}
	})

	t.Run("resolve exec error propagates", func(t *testing.T) {
		db, mock := newConflictMock(t)
		mock.ExpectQuery(selQ).WillReturnRows(
			sqlmock.NewRows([]string{"our_version", "our_content_hash", "their_content_hash"}).
				AddRow(53, "have", "have"))
		mock.ExpectExec(regexp.QuoteMeta("CALL DOLT_CONFLICTS_RESOLVE('--ours', 'schema_migrations')")).
			WillReturnError(errors.New("boom"))
		if err := resolveSchemaMigrationsVintageConflicts(context.Background(), db); err == nil {
			t.Fatal("expected resolve error")
		}
	})
}

// --- constraintViolationTables & violationsAreIssueFKOnly --------------------

func TestConstraintViolationTables(t *testing.T) {
	t.Parallel()
	q := regexp.QuoteMeta("SELECT `table` FROM dolt_constraint_violations WHERE num_violations > 0")

	t.Run("returns violating tables", func(t *testing.T) {
		db, mock := newConflictMock(t)
		mock.ExpectQuery(q).WillReturnRows(sqlmock.NewRows([]string{"table"}).
			AddRow("dependencies").AddRow("labels"))
		tables, err := constraintViolationTables(context.Background(), db)
		if err != nil {
			t.Fatalf("constraintViolationTables: %v", err)
		}
		if len(tables) != 2 || tables[0] != "dependencies" || tables[1] != "labels" {
			t.Fatalf("tables = %v, want [dependencies labels]", tables)
		}
	})

	t.Run("query error propagates", func(t *testing.T) {
		db, mock := newConflictMock(t)
		mock.ExpectQuery(q).WillReturnError(errors.New("boom"))
		if _, err := constraintViolationTables(context.Background(), db); err == nil {
			t.Fatal("expected query error")
		}
	})

	t.Run("scan error propagates", func(t *testing.T) {
		db, mock := newConflictMock(t)
		mock.ExpectQuery(q).WillReturnRows(sqlmock.NewRows([]string{"a", "b"}).AddRow("x", "y"))
		if _, err := constraintViolationTables(context.Background(), db); err == nil {
			t.Fatal("expected scan error")
		}
	})
}

func TestViolationsAreIssueFKOnly(t *testing.T) {
	t.Parallel()
	q := regexp.QuoteMeta("SELECT violation_type, violation_info FROM dolt_constraint_violations_dependencies")
	cols := []string{"violation_type", "violation_info"}

	t.Run("issue FK violation is issue-only", func(t *testing.T) {
		db, mock := newConflictMock(t)
		mock.ExpectQuery(q).WillReturnRows(sqlmock.NewRows(cols).
			AddRow("foreign key", `{"ReferencedTable":"issues"}`))
		ok, err := violationsAreIssueFKOnly(context.Background(), db, "dependencies")
		if err != nil || !ok {
			t.Fatalf("got ok=%v err=%v, want true,nil", ok, err)
		}
	})

	t.Run("info as []byte also parses", func(t *testing.T) {
		db, mock := newConflictMock(t)
		mock.ExpectQuery(q).WillReturnRows(sqlmock.NewRows(cols).
			AddRow("foreign key", []byte(`{"ReferencedTable":"issues"}`)))
		ok, err := violationsAreIssueFKOnly(context.Background(), db, "dependencies")
		if err != nil || !ok {
			t.Fatalf("got ok=%v err=%v, want true,nil", ok, err)
		}
	})

	t.Run("non-FK violation type is not issue-only", func(t *testing.T) {
		db, mock := newConflictMock(t)
		mock.ExpectQuery(q).WillReturnRows(sqlmock.NewRows(cols).
			AddRow("unique key", `{"ReferencedTable":"issues"}`))
		ok, err := violationsAreIssueFKOnly(context.Background(), db, "dependencies")
		if err != nil || ok {
			t.Fatalf("got ok=%v err=%v, want false,nil", ok, err)
		}
	})

	t.Run("FK to a different parent is not issue-only", func(t *testing.T) {
		db, mock := newConflictMock(t)
		mock.ExpectQuery(q).WillReturnRows(sqlmock.NewRows(cols).
			AddRow("foreign key", `{"ReferencedTable":"wisps"}`))
		ok, err := violationsAreIssueFKOnly(context.Background(), db, "dependencies")
		if err != nil || ok {
			t.Fatalf("got ok=%v err=%v, want false,nil", ok, err)
		}
	})

	t.Run("unparseable descriptor is not issue-only", func(t *testing.T) {
		db, mock := newConflictMock(t)
		mock.ExpectQuery(q).WillReturnRows(sqlmock.NewRows(cols).
			AddRow("foreign key", `not json`))
		ok, err := violationsAreIssueFKOnly(context.Background(), db, "dependencies")
		if err != nil || ok {
			t.Fatalf("got ok=%v err=%v, want false,nil", ok, err)
		}
	})

	t.Run("unsafe identifier is rejected before any query", func(t *testing.T) {
		db, _ := newConflictMock(t)
		if _, err := violationsAreIssueFKOnly(context.Background(), db, "a b"); err == nil {
			t.Fatal("expected identifier error")
		}
	})

	t.Run("query error propagates", func(t *testing.T) {
		db, mock := newConflictMock(t)
		mock.ExpectQuery(q).WillReturnError(errors.New("boom"))
		if _, err := violationsAreIssueFKOnly(context.Background(), db, "dependencies"); err == nil {
			t.Fatal("expected query error")
		}
	})

	t.Run("scan error propagates", func(t *testing.T) {
		db, mock := newConflictMock(t)
		mock.ExpectQuery(q).WillReturnRows(sqlmock.NewRows([]string{"violation_type"}).AddRow("foreign key"))
		if _, err := violationsAreIssueFKOnly(context.Background(), db, "dependencies"); err == nil {
			t.Fatal("expected scan error")
		}
	})
}

// --- TryRepairFKCascadeViolations --------------------------------------------

func TestTryRepairFKCascadeViolations(t *testing.T) {
	t.Parallel()
	listQ := regexp.QuoteMeta("SELECT `table` FROM dolt_constraint_violations WHERE num_violations > 0")

	t.Run("no violations is a no-op", func(t *testing.T) {
		db, mock := newConflictMock(t)
		mock.ExpectQuery(listQ).WillReturnRows(sqlmock.NewRows([]string{"table"}))
		repaired, had, err := TryRepairFKCascadeViolations(context.Background(), db)
		if err != nil || repaired || had {
			t.Fatalf("got repaired=%v had=%v err=%v, want false,false,nil", repaired, had, err)
		}
	})

	t.Run("list error propagates", func(t *testing.T) {
		db, mock := newConflictMock(t)
		mock.ExpectQuery(listQ).WillReturnError(errors.New("boom"))
		if _, _, err := TryRepairFKCascadeViolations(context.Background(), db); err == nil {
			t.Fatal("expected list error")
		}
	})

	t.Run("unknown violating table is left untouched", func(t *testing.T) {
		db, mock := newConflictMock(t)
		mock.ExpectQuery(listQ).WillReturnRows(sqlmock.NewRows([]string{"table"}).AddRow("mystery_table"))
		repaired, had, err := TryRepairFKCascadeViolations(context.Background(), db)
		if err != nil || repaired || !had {
			t.Fatalf("got repaired=%v had=%v err=%v, want false,true,nil", repaired, had, err)
		}
	})

	t.Run("non-issue-FK violation is left untouched", func(t *testing.T) {
		db, mock := newConflictMock(t)
		mock.ExpectQuery(listQ).WillReturnRows(sqlmock.NewRows([]string{"table"}).AddRow("labels"))
		mock.ExpectQuery(regexp.QuoteMeta("FROM dolt_constraint_violations_labels")).
			WillReturnRows(sqlmock.NewRows([]string{"violation_type", "violation_info"}).
				AddRow("unique key", `{}`))
		repaired, had, err := TryRepairFKCascadeViolations(context.Background(), db)
		if err != nil || repaired || !had {
			t.Fatalf("got repaired=%v had=%v err=%v, want false,true,nil", repaired, had, err)
		}
	})

	t.Run("issue-FK violation is repaired and cleared", func(t *testing.T) {
		db, mock := newConflictMock(t)
		// 1) list violating tables
		mock.ExpectQuery(listQ).WillReturnRows(sqlmock.NewRows([]string{"table"}).AddRow("labels"))
		// 2) validate it's issue-FK only
		mock.ExpectQuery(regexp.QuoteMeta("FROM dolt_constraint_violations_labels")).
			WillReturnRows(sqlmock.NewRows([]string{"violation_type", "violation_info"}).
				AddRow("foreign key", `{"ReferencedTable":"issues"}`))
		// 3) cascade-delete dangling rows
		mock.ExpectExec(regexp.QuoteMeta("DELETE FROM labels WHERE issue_id NOT IN")).
			WillReturnResult(sqlmock.NewResult(0, 2))
		// 4) clear the violation table
		mock.ExpectExec(regexp.QuoteMeta("DELETE FROM dolt_constraint_violations_labels")).
			WillReturnResult(sqlmock.NewResult(0, 2))
		// 5) stage the repaired table
		mock.ExpectExec(regexp.QuoteMeta("CALL DOLT_ADD('labels')")).
			WillReturnResult(sqlmock.NewResult(0, 0))
		// 6) re-check: nothing left
		mock.ExpectQuery(listQ).WillReturnRows(sqlmock.NewRows([]string{"table"}))
		repaired, had, err := TryRepairFKCascadeViolations(context.Background(), db)
		if err != nil || !repaired || !had {
			t.Fatalf("got repaired=%v had=%v err=%v, want true,true,nil", repaired, had, err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet expectations: %v", err)
		}
	})

	t.Run("residual violation after repair is not kept", func(t *testing.T) {
		db, mock := newConflictMock(t)
		mock.ExpectQuery(listQ).WillReturnRows(sqlmock.NewRows([]string{"table"}).AddRow("labels"))
		mock.ExpectQuery(regexp.QuoteMeta("FROM dolt_constraint_violations_labels")).
			WillReturnRows(sqlmock.NewRows([]string{"violation_type", "violation_info"}).
				AddRow("foreign key", `{"ReferencedTable":"issues"}`))
		mock.ExpectExec(regexp.QuoteMeta("DELETE FROM labels WHERE issue_id NOT IN")).
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectExec(regexp.QuoteMeta("DELETE FROM dolt_constraint_violations_labels")).
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectExec(regexp.QuoteMeta("CALL DOLT_ADD('labels')")).
			WillReturnResult(sqlmock.NewResult(0, 0))
		// re-check still reports a violation -> not kept
		mock.ExpectQuery(listQ).WillReturnRows(sqlmock.NewRows([]string{"table"}).AddRow("labels"))
		repaired, had, err := TryRepairFKCascadeViolations(context.Background(), db)
		if err != nil || repaired || !had {
			t.Fatalf("got repaired=%v had=%v err=%v, want false,true,nil", repaired, had, err)
		}
	})

	t.Run("cascade-delete exec error propagates", func(t *testing.T) {
		db, mock := newConflictMock(t)
		mock.ExpectQuery(listQ).WillReturnRows(sqlmock.NewRows([]string{"table"}).AddRow("labels"))
		mock.ExpectQuery(regexp.QuoteMeta("FROM dolt_constraint_violations_labels")).
			WillReturnRows(sqlmock.NewRows([]string{"violation_type", "violation_info"}).
				AddRow("foreign key", `{"ReferencedTable":"issues"}`))
		mock.ExpectExec(regexp.QuoteMeta("DELETE FROM labels WHERE issue_id NOT IN")).
			WillReturnError(errors.New("boom"))
		repaired, had, err := TryRepairFKCascadeViolations(context.Background(), db)
		if err == nil || repaired || !had {
			t.Fatalf("got repaired=%v had=%v err=%v, want false,true,err", repaired, had, err)
		}
	})
}
