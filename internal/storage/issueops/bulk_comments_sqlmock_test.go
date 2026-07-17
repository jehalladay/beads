package issueops

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestGetCommentsForIssuesInTx(t *testing.T) {
	t.Parallel()

	probe := regexp.QuoteMeta("SELECT 1 FROM wisps LIMIT 1")
	wispIn := regexp.QuoteMeta("SELECT id FROM wisps WHERE id IN (?,?)")
	commentsQ := `SELECT id, issue_id, author, text, created_at\s+FROM comments\s+WHERE issue_id IN \(\S+\)`
	wispCommentsQ := `SELECT id, issue_id, author, text, created_at\s+FROM wisp_comments\s+WHERE issue_id IN \(\S+\)`
	cols := []string{"id", "issue_id", "author", "text", "created_at"}
	now := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)

	t.Run("empty ids -> empty map, no queries", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		got, err := GetCommentsForIssuesInTx(context.Background(), tx, nil)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("got %v, want empty map", got)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet: %v", err)
		}
	})

	t.Run("perm-only ids fetch from comments table", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		// wisps table empty -> all perm
		mock.ExpectQuery(probe).WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow(1))
		mock.ExpectQuery(wispIn).WithArgs("bd-1", "bd-2").
			WillReturnRows(sqlmock.NewRows([]string{"id"})) // no wisp membership
		mock.ExpectQuery(commentsQ).WithArgs("bd-1", "bd-2").
			WillReturnRows(sqlmock.NewRows(cols).
				AddRow("c1", "bd-1", "alice", "hi", now).
				AddRow("c2", "bd-2", "bob", "yo", now))

		got, err := GetCommentsForIssuesInTx(context.Background(), tx, []string{"bd-1", "bd-2"})
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if len(got["bd-1"]) != 1 || got["bd-1"][0].ID != "c1" || len(got["bd-2"]) != 1 || got["bd-2"][0].Author != "bob" {
			t.Fatalf("got %+v, want c1 on bd-1 and c2/bob on bd-2", got)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet: %v", err)
		}
	})

	t.Run("mixed wisp + perm ids hit both tables", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery(probe).WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow(1))
		mock.ExpectQuery(wispIn).WithArgs("bd-1", "w-1").
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("w-1"))
		// perm branch first (permIDs=[bd-1]), then wisp branch (wispIDs=[w-1])
		mock.ExpectQuery(commentsQ).WithArgs("bd-1").
			WillReturnRows(sqlmock.NewRows(cols).AddRow("c1", "bd-1", "alice", "hi", now))
		mock.ExpectQuery(wispCommentsQ).WithArgs("w-1").
			WillReturnRows(sqlmock.NewRows(cols).AddRow("wc1", "w-1", "carol", "wisp", now))

		got, err := GetCommentsForIssuesInTx(context.Background(), tx, []string{"bd-1", "w-1"})
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if got["bd-1"][0].ID != "c1" || got["w-1"][0].ID != "wc1" {
			t.Fatalf("got %+v, want c1 on bd-1 and wc1 on w-1", got)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet: %v", err)
		}
	})

	t.Run("partition error propagates", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery(probe).WillReturnError(errors.New("probe boom"))
		if _, err := GetCommentsForIssuesInTx(context.Background(), tx, []string{"bd-1"}); err == nil {
			t.Fatal("expected partition error")
		}
	})

	t.Run("comments query error propagates", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery(probe).WillReturnError(sql.ErrNoRows) // empty -> all perm
		mock.ExpectQuery(commentsQ).WithArgs("bd-1").WillReturnError(errors.New("query boom"))
		if _, err := GetCommentsForIssuesInTx(context.Background(), tx, []string{"bd-1"}); err == nil {
			t.Fatal("expected comments query error")
		}
	})

	t.Run("comment scan error propagates", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery(probe).WillReturnError(sql.ErrNoRows)
		rows := sqlmock.NewRows(cols).
			AddRow("c1", "bd-1", "alice", "hi", now).RowError(0, errors.New("row broke"))
		mock.ExpectQuery(commentsQ).WithArgs("bd-1").WillReturnRows(rows)
		if _, err := GetCommentsForIssuesInTx(context.Background(), tx, []string{"bd-1"}); err == nil {
			t.Fatal("expected scan/rows error")
		}
	})
}
