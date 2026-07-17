package issueops

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/steveyegge/beads/internal/storage"
)

func TestGetIssueByExternalRefInTx(t *testing.T) {
	t.Parallel()

	issuesQ := regexp.QuoteMeta("SELECT id FROM issues WHERE external_ref = ?")
	wispsQ := regexp.QuoteMeta("SELECT id FROM wisps WHERE external_ref = ?")

	t.Run("found in issues table", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery(issuesQ).WithArgs("ext-1").
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("bd-1"))
		got, err := GetIssueByExternalRefInTx(context.Background(), tx, "ext-1")
		if err != nil || got != "bd-1" {
			t.Fatalf("got (%q,%v), want (bd-1,nil)", got, err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet expectations: %v", err)
		}
	})

	t.Run("missing in issues falls back to wisps", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery(issuesQ).WithArgs("ext-2").WillReturnError(sql.ErrNoRows)
		mock.ExpectQuery(wispsQ).WithArgs("ext-2").
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("bd-w"))
		got, err := GetIssueByExternalRefInTx(context.Background(), tx, "ext-2")
		if err != nil || got != "bd-w" {
			t.Fatalf("got (%q,%v), want (bd-w,nil)", got, err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet expectations: %v", err)
		}
	})

	t.Run("missing in both returns ErrNotFound", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery(issuesQ).WithArgs("ext-x").WillReturnError(sql.ErrNoRows)
		mock.ExpectQuery(wispsQ).WithArgs("ext-x").WillReturnError(sql.ErrNoRows)
		_, err := GetIssueByExternalRefInTx(context.Background(), tx, "ext-x")
		if !errors.Is(err, storage.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("non-notfound error from issues query is wrapped", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery(issuesQ).WithArgs("ext-e").WillReturnError(errors.New("boom"))
		_, err := GetIssueByExternalRefInTx(context.Background(), tx, "ext-e")
		if err == nil || errors.Is(err, storage.ErrNotFound) {
			t.Fatalf("err = %v, want a wrapped non-notfound error", err)
		}
	})
}

func TestGetRepoMtimeInTx(t *testing.T) {
	t.Parallel()

	q := regexp.QuoteMeta("SELECT mtime_ns FROM repo_mtimes WHERE repo_path = ?")

	t.Run("returns stored mtime", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery(q).WithArgs("/repo").
			WillReturnRows(sqlmock.NewRows([]string{"mtime_ns"}).AddRow(int64(12345)))
		got, err := GetRepoMtimeInTx(context.Background(), tx, "/repo")
		if err != nil || got != 12345 {
			t.Fatalf("got (%d,%v), want (12345,nil)", got, err)
		}
	})

	t.Run("missing row returns 0,nil by design", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery(q).WithArgs("/none").WillReturnError(sql.ErrNoRows)
		got, err := GetRepoMtimeInTx(context.Background(), tx, "/none")
		if err != nil || got != 0 {
			t.Fatalf("got (%d,%v), want (0,nil) — missing mtime is not an error", got, err)
		}
	})
}

func TestSetRepoMtimeInTx(t *testing.T) {
	t.Parallel()

	t.Run("upsert succeeds", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		mock.ExpectExec("INSERT INTO repo_mtimes").
			WithArgs("/repo", "/repo/.beads/issues.jsonl", int64(99)).
			WillReturnResult(sqlmock.NewResult(1, 1))
		if err := SetRepoMtimeInTx(context.Background(), tx, "/repo", "/repo/.beads/issues.jsonl", 99); err != nil {
			t.Fatalf("SetRepoMtimeInTx: %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet expectations: %v", err)
		}
	})

	t.Run("exec error is wrapped", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		mock.ExpectExec("INSERT INTO repo_mtimes").WillReturnError(errors.New("boom"))
		err := SetRepoMtimeInTx(context.Background(), tx, "/repo", "/j", 1)
		if err == nil {
			t.Fatal("expected wrapped error")
		}
	})
}

func TestClearRepoMtimeInTx(t *testing.T) {
	t.Parallel()

	t.Run("deletes by absolute-resolved path", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		// The stored path is expandAndAbsPath(repoPath); an already-absolute
		// path is preserved verbatim, so we can assert the exact arg.
		mock.ExpectExec(regexp.QuoteMeta("DELETE FROM repo_mtimes WHERE repo_path = ?")).
			WithArgs("/abs/repo").
			WillReturnResult(sqlmock.NewResult(0, 1))
		if err := ClearRepoMtimeInTx(context.Background(), tx, "/abs/repo"); err != nil {
			t.Fatalf("ClearRepoMtimeInTx: %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet expectations: %v", err)
		}
	})

	t.Run("exec error is wrapped", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		mock.ExpectExec("DELETE FROM repo_mtimes").WillReturnError(errors.New("boom"))
		if err := ClearRepoMtimeInTx(context.Background(), tx, "/abs/repo"); err == nil {
			t.Fatal("expected wrapped error")
		}
	})
}

func TestDeleteConfigInTx(t *testing.T) {
	t.Parallel()

	t.Run("deletes the key", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		mock.ExpectExec(regexp.QuoteMeta("DELETE FROM config WHERE `key` = ?")).
			WithArgs("some.key").
			WillReturnResult(sqlmock.NewResult(0, 1))
		if err := DeleteConfigInTx(context.Background(), tx, "some.key"); err != nil {
			t.Fatalf("DeleteConfigInTx: %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet expectations: %v", err)
		}
	})

	t.Run("exec error is wrapped", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		mock.ExpectExec("DELETE FROM config").WillReturnError(errors.New("boom"))
		if err := DeleteConfigInTx(context.Background(), tx, "some.key"); err == nil {
			t.Fatal("expected wrapped error")
		}
	})
}
