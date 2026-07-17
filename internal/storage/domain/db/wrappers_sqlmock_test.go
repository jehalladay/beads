package db

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

// These tests cover the thin SQL wrappers in dolt.go, remote.go, and ddl.go
// hermetically via sqlmock. The DOLT_* version-control verbs and the DDL
// helpers only forward to Runner.ExecContext/QueryContext, so a real Dolt
// container isn't needed — we assert the exact query text, argument
// forwarding, and error-wrapping instead (beads-5w7x).

// newMockRunner returns a *sql.DB (which satisfies Runner) backed by sqlmock.
// QueryMatcherEqual makes ExpectExec/ExpectQuery match the query string
// verbatim, so we verify the precise SQL each wrapper emits.
func newMockRunner(t *testing.T) (Runner, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("unmet sqlmock expectations: %v", err)
		}
	})
	return db, mock
}

// ---- dolt.go: DoltVersionControlSQLRepository ----

// doltVerb pairs a wrapper method with the stored procedure it must CALL.
type doltVerb struct {
	name string
	proc string
	call func(context.Context, DoltVersionControlSQLRepository) error
}

func doltVerbs() []doltVerb {
	return []doltVerb{
		{"Checkout", "DOLT_CHECKOUT", func(ctx context.Context, r DoltVersionControlSQLRepository) error {
			return r.Checkout(ctx, "-b", "feature")
		}},
		{"Branch", "DOLT_BRANCH", func(ctx context.Context, r DoltVersionControlSQLRepository) error { return r.Branch(ctx, "feature") }},
		{"Add", "DOLT_ADD", func(ctx context.Context, r DoltVersionControlSQLRepository) error { return r.Add(ctx, "-A") }},
		{"Commit", "DOLT_COMMIT", func(ctx context.Context, r DoltVersionControlSQLRepository) error { return r.Commit(ctx, "-m", "msg") }},
		{"Merge", "DOLT_MERGE", func(ctx context.Context, r DoltVersionControlSQLRepository) error { return r.Merge(ctx, "feature") }},
		{"Remote", "DOLT_REMOTE", func(ctx context.Context, r DoltVersionControlSQLRepository) error {
			return r.Remote(ctx, "add", "origin", "file:///tmp/x")
		}},
		{"Fetch", "DOLT_FETCH", func(ctx context.Context, r DoltVersionControlSQLRepository) error { return r.Fetch(ctx, "origin") }},
		{"Push", "DOLT_PUSH", func(ctx context.Context, r DoltVersionControlSQLRepository) error {
			return r.Push(ctx, "origin", "main")
		}},
		{"Pull", "DOLT_PULL", func(ctx context.Context, r DoltVersionControlSQLRepository) error { return r.Pull(ctx, "origin") }},
		{"Clone", "DOLT_CLONE", func(ctx context.Context, r DoltVersionControlSQLRepository) error {
			return r.Clone(ctx, "file:///tmp/x", "db")
		}},
	}
}

func TestDoltVersionControl_AllVerbs_Success(t *testing.T) {
	t.Parallel()
	for _, v := range doltVerbs() {
		v := v
		t.Run(v.name, func(t *testing.T) {
			t.Parallel()
			runner, mock := newMockRunner(t)
			repo := NewDoltVersionControlSQLRepository(runner)

			// Each verb forwards its args as placeholders into the proc call.
			// The exact number of args differs per verb; match the proc name
			// prefix and accept any args via WithArgs matching is awkward with
			// verbatim matcher, so assert the full query text per verb.
			mock.ExpectExec(expectedProcQuery(v)).WillReturnResult(sqlmock.NewResult(0, 0))

			if err := v.call(context.Background(), repo); err != nil {
				t.Fatalf("%s: unexpected error: %v", v.name, err)
			}
		})
	}
}

func TestDoltVersionControl_AllVerbs_ErrorWrapped(t *testing.T) {
	t.Parallel()
	for _, v := range doltVerbs() {
		v := v
		t.Run(v.name, func(t *testing.T) {
			t.Parallel()
			runner, mock := newMockRunner(t)
			repo := NewDoltVersionControlSQLRepository(runner)

			mock.ExpectExec(expectedProcQuery(v)).WillReturnError(errors.New("boom"))

			err := v.call(context.Background(), repo)
			if err == nil {
				t.Fatalf("%s: expected error, got nil", v.name)
			}
			// call() wraps as "db: <PROC>: <cause>".
			if want := "db: " + v.proc + ": "; !strings.HasPrefix(err.Error(), want) {
				t.Fatalf("%s: error %q does not start with %q", v.name, err.Error(), want)
			}
			if !strings.Contains(err.Error(), "boom") {
				t.Fatalf("%s: error %q does not wrap the cause", v.name, err.Error())
			}
		})
	}
}

// expectedProcQuery reconstructs the exact "CALL PROC(?, ?, ...)" string a verb
// emits, so the verbatim matcher lines up with call()'s placeholder builder.
func expectedProcQuery(v doltVerb) string {
	nArgs := map[string]int{
		"DOLT_CHECKOUT": 2, "DOLT_BRANCH": 1, "DOLT_ADD": 1, "DOLT_COMMIT": 2,
		"DOLT_MERGE": 1, "DOLT_REMOTE": 3, "DOLT_FETCH": 1, "DOLT_PUSH": 2,
		"DOLT_PULL": 1, "DOLT_CLONE": 2,
	}[v.proc]
	ph := ""
	for i := 0; i < nArgs; i++ {
		if i > 0 {
			ph += ", "
		}
		ph += "?"
	}
	return "CALL " + v.proc + "(" + ph + ")"
}

// TestDoltVersionControl_NoArgs verifies call() builds an empty parameter list
// (no placeholders) when a verb is invoked without args.
func TestDoltVersionControl_NoArgs(t *testing.T) {
	t.Parallel()
	runner, mock := newMockRunner(t)
	repo := NewDoltVersionControlSQLRepository(runner)

	mock.ExpectExec("CALL DOLT_ADD()").WillReturnResult(sqlmock.NewResult(0, 0))
	if err := repo.Add(context.Background()); err != nil {
		t.Fatalf("Add() no-args: %v", err)
	}
}

// ---- remote.go: RemoteSQLRepository ----

func TestRemote_AddRemote(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		runner, mock := newMockRunner(t)
		repo := NewRemoteSQLRepository(runner)
		mock.ExpectExec("CALL DOLT_REMOTE(?, ?, ?)").
			WithArgs("add", "origin", "file:///tmp/x").
			WillReturnResult(sqlmock.NewResult(0, 0))
		if err := repo.AddRemote(context.Background(), "origin", "file:///tmp/x"); err != nil {
			t.Fatalf("AddRemote: %v", err)
		}
	})

	t.Run("error wrapped", func(t *testing.T) {
		t.Parallel()
		runner, mock := newMockRunner(t)
		repo := NewRemoteSQLRepository(runner)
		mock.ExpectExec("CALL DOLT_REMOTE(?, ?, ?)").WillReturnError(errors.New("boom"))
		err := repo.AddRemote(context.Background(), "origin", "file:///tmp/x")
		if err == nil || !strings.Contains(err.Error(), "db: AddRemote origin") {
			t.Fatalf("expected wrapped AddRemote error, got %v", err)
		}
	})
}

func TestRemote_RemoveRemote(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		runner, mock := newMockRunner(t)
		repo := NewRemoteSQLRepository(runner)
		mock.ExpectExec("CALL DOLT_REMOTE(?, ?)").
			WithArgs("remove", "origin").
			WillReturnResult(sqlmock.NewResult(0, 0))
		if err := repo.RemoveRemote(context.Background(), "origin"); err != nil {
			t.Fatalf("RemoveRemote: %v", err)
		}
	})

	t.Run("error wrapped", func(t *testing.T) {
		t.Parallel()
		runner, mock := newMockRunner(t)
		repo := NewRemoteSQLRepository(runner)
		mock.ExpectExec("CALL DOLT_REMOTE(?, ?)").WillReturnError(errors.New("boom"))
		err := repo.RemoveRemote(context.Background(), "origin")
		if err == nil || !strings.Contains(err.Error(), "db: RemoveRemote origin") {
			t.Fatalf("expected wrapped RemoveRemote error, got %v", err)
		}
	})
}

func TestRemote_ListRemotes(t *testing.T) {
	t.Parallel()
	const q = "SELECT name, url FROM dolt_remotes"

	t.Run("rows returned", func(t *testing.T) {
		t.Parallel()
		runner, mock := newMockRunner(t)
		repo := NewRemoteSQLRepository(runner)
		mock.ExpectQuery(q).WillReturnRows(
			sqlmock.NewRows([]string{"name", "url"}).
				AddRow("origin", "file:///a").
				AddRow("backup", "file:///b"),
		)
		got, err := repo.ListRemotes(context.Background())
		if err != nil {
			t.Fatalf("ListRemotes: %v", err)
		}
		if len(got) != 2 || got[0].Name != "origin" || got[0].URL != "file:///a" || got[1].Name != "backup" {
			t.Fatalf("unexpected remotes: %+v", got)
		}
	})

	t.Run("empty", func(t *testing.T) {
		t.Parallel()
		runner, mock := newMockRunner(t)
		repo := NewRemoteSQLRepository(runner)
		mock.ExpectQuery(q).WillReturnRows(sqlmock.NewRows([]string{"name", "url"}))
		got, err := repo.ListRemotes(context.Background())
		if err != nil {
			t.Fatalf("ListRemotes: %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("expected no remotes, got %+v", got)
		}
	})

	t.Run("query error", func(t *testing.T) {
		t.Parallel()
		runner, mock := newMockRunner(t)
		repo := NewRemoteSQLRepository(runner)
		mock.ExpectQuery(q).WillReturnError(errors.New("boom"))
		_, err := repo.ListRemotes(context.Background())
		if err == nil || !strings.Contains(err.Error(), "db: ListRemotes: query") {
			t.Fatalf("expected wrapped query error, got %v", err)
		}
	})

	t.Run("scan error", func(t *testing.T) {
		t.Parallel()
		runner, mock := newMockRunner(t)
		repo := NewRemoteSQLRepository(runner)
		// One column instead of two forces Scan(&Name, &URL) to fail.
		mock.ExpectQuery(q).WillReturnRows(
			sqlmock.NewRows([]string{"name"}).AddRow("origin"),
		)
		_, err := repo.ListRemotes(context.Background())
		if err == nil || !strings.Contains(err.Error(), "db: ListRemotes: scan") {
			t.Fatalf("expected wrapped scan error, got %v", err)
		}
	})

	t.Run("rows iteration error", func(t *testing.T) {
		t.Parallel()
		runner, mock := newMockRunner(t)
		repo := NewRemoteSQLRepository(runner)
		mock.ExpectQuery(q).WillReturnRows(
			sqlmock.NewRows([]string{"name", "url"}).
				AddRow("origin", "file:///a").
				RowError(0, errors.New("row boom")),
		)
		_, err := repo.ListRemotes(context.Background())
		if err == nil || !strings.Contains(err.Error(), "db: ListRemotes: rows") {
			t.Fatalf("expected wrapped rows error, got %v", err)
		}
	})
}

// ---- ddl.go: DDLSQLRepository + quoteIdentifier ----

func TestDDL_CreateDatabaseIfNotExists(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		runner, mock := newMockRunner(t)
		repo := NewDDLSQLRepository(runner)
		mock.ExpectExec("CREATE DATABASE IF NOT EXISTS `mydb`").
			WillReturnResult(sqlmock.NewResult(0, 0))
		if err := repo.CreateDatabaseIfNotExists(context.Background(), "mydb"); err != nil {
			t.Fatalf("CreateDatabaseIfNotExists: %v", err)
		}
	})

	t.Run("invalid identifier rejected before any query", func(t *testing.T) {
		t.Parallel()
		runner, _ := newMockRunner(t)
		repo := NewDDLSQLRepository(runner)
		// No ExpectExec: quoteIdentifier must reject before touching the DB.
		err := repo.CreateDatabaseIfNotExists(context.Background(), "bad name;DROP")
		if err == nil || !strings.Contains(err.Error(), "invalid identifier") {
			t.Fatalf("expected invalid identifier error, got %v", err)
		}
	})

	t.Run("exec error wrapped", func(t *testing.T) {
		t.Parallel()
		runner, mock := newMockRunner(t)
		repo := NewDDLSQLRepository(runner)
		mock.ExpectExec("CREATE DATABASE IF NOT EXISTS `mydb`").WillReturnError(errors.New("boom"))
		err := repo.CreateDatabaseIfNotExists(context.Background(), "mydb")
		if err == nil || !strings.Contains(err.Error(), "db: CreateDatabaseIfNotExists") {
			t.Fatalf("expected wrapped exec error, got %v", err)
		}
	})
}

func TestDDL_UseDatabase(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		runner, mock := newMockRunner(t)
		repo := NewDDLSQLRepository(runner)
		mock.ExpectExec("USE `mydb`").WillReturnResult(sqlmock.NewResult(0, 0))
		if err := repo.UseDatabase(context.Background(), "mydb"); err != nil {
			t.Fatalf("UseDatabase: %v", err)
		}
	})

	t.Run("invalid identifier rejected", func(t *testing.T) {
		t.Parallel()
		runner, _ := newMockRunner(t)
		repo := NewDDLSQLRepository(runner)
		err := repo.UseDatabase(context.Background(), "1bad")
		if err == nil || !strings.Contains(err.Error(), "invalid identifier") {
			t.Fatalf("expected invalid identifier error, got %v", err)
		}
	})

	t.Run("exec error wrapped", func(t *testing.T) {
		t.Parallel()
		runner, mock := newMockRunner(t)
		repo := NewDDLSQLRepository(runner)
		mock.ExpectExec("USE `mydb`").WillReturnError(errors.New("boom"))
		err := repo.UseDatabase(context.Background(), "mydb")
		if err == nil || !strings.Contains(err.Error(), "db: UseDatabase") {
			t.Fatalf("expected wrapped exec error, got %v", err)
		}
	})
}

func TestQuoteIdentifier(t *testing.T) {
	t.Parallel()
	valid := []string{"mydb", "_underscore", "Mixed_Case9", "a"}
	for _, name := range valid {
		got, err := quoteIdentifier(name)
		if err != nil {
			t.Errorf("quoteIdentifier(%q) unexpected error: %v", name, err)
			continue
		}
		if want := "`" + name + "`"; got != want {
			t.Errorf("quoteIdentifier(%q) = %q, want %q", name, got, want)
		}
	}

	invalid := []string{"", "1leading", "has space", "semi;colon", "dash-name", "back`tick", "quote'"}
	for _, name := range invalid {
		if _, err := quoteIdentifier(name); err == nil {
			t.Errorf("quoteIdentifier(%q) expected error, got nil", name)
		}
	}
}
