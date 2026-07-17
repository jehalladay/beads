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

// expectNotWisp mocks IsActiveWispInTx's probe so routing selects the regular
// (non-wisp) tables: SELECT 1 FROM wisps WHERE id = ? LIMIT 1 -> no rows.
func expectNotWisp(mock sqlmock.Sqlmock, id string) {
	mock.ExpectQuery(regexp.QuoteMeta("SELECT 1 FROM wisps WHERE id = ? LIMIT 1")).
		WithArgs(id).WillReturnError(sql.ErrNoRows)
}

func TestGetIssueCommentsInTx(t *testing.T) {
	t.Parallel()

	commentsQ := regexp.QuoteMeta("SELECT id, issue_id, author, text, created_at") // FROM comments...

	t.Run("returns comments from the regular table", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		expectNotWisp(mock, "bd-1")
		now := time.Now().UTC()
		mock.ExpectQuery(commentsQ).WithArgs("bd-1").
			WillReturnRows(sqlmock.NewRows([]string{"id", "issue_id", "author", "text", "created_at"}).
				AddRow("c1", "bd-1", "alice", "first", now).
				AddRow("c2", "bd-1", "bob", "second", now))
		got, err := GetIssueCommentsInTx(context.Background(), tx, "bd-1")
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if len(got) != 2 || got[0].ID != "c1" || got[1].Author != "bob" {
			t.Fatalf("comments = %+v, want 2 rows [c1, bob]", got)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet expectations: %v", err)
		}
	})

	t.Run("query error is wrapped", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		expectNotWisp(mock, "bd-1")
		mock.ExpectQuery(commentsQ).WithArgs("bd-1").WillReturnError(errors.New("boom"))
		if _, err := GetIssueCommentsInTx(context.Background(), tx, "bd-1"); err == nil {
			t.Fatal("expected wrapped query error")
		}
	})
}

func TestImportIssueCommentInTx(t *testing.T) {
	t.Parallel()

	existsQ := regexp.QuoteMeta("SELECT EXISTS(SELECT 1 FROM issues WHERE id = ?)")
	insertQ := regexp.QuoteMeta("INSERT INTO comments (id, issue_id, author, text, created_at)")
	ts := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)

	t.Run("inserts a comment for an existing issue", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		expectNotWisp(mock, "bd-1")
		mock.ExpectQuery(existsQ).WithArgs("bd-1").
			WillReturnRows(sqlmock.NewRows([]string{"e"}).AddRow(true))
		mock.ExpectExec(insertQ).
			WithArgs(sqlmock.AnyArg(), "bd-1", "alice", "hello", ts).
			WillReturnResult(sqlmock.NewResult(1, 1))
		c, err := ImportIssueCommentInTx(context.Background(), tx, "bd-1", "alice", "hello", ts)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if c.ID == "" || c.IssueID != "bd-1" || c.Author != "alice" || c.Text != "hello" {
			t.Fatalf("comment = %+v, want populated with a generated id", c)
		}
		if !c.CreatedAt.Equal(ts) {
			t.Fatalf("CreatedAt = %v, want %v", c.CreatedAt, ts)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet expectations: %v", err)
		}
	})

	t.Run("missing issue returns not-found error (no insert)", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		expectNotWisp(mock, "bd-x")
		mock.ExpectQuery(existsQ).WithArgs("bd-x").
			WillReturnRows(sqlmock.NewRows([]string{"e"}).AddRow(false))
		if _, err := ImportIssueCommentInTx(context.Background(), tx, "bd-x", "a", "t", ts); err == nil {
			t.Fatal("expected not-found error")
		}
	})

	t.Run("insert error is wrapped", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		expectNotWisp(mock, "bd-1")
		mock.ExpectQuery(existsQ).WithArgs("bd-1").
			WillReturnRows(sqlmock.NewRows([]string{"e"}).AddRow(true))
		mock.ExpectExec(insertQ).WillReturnError(errors.New("boom"))
		if _, err := ImportIssueCommentInTx(context.Background(), tx, "bd-1", "a", "t", ts); err == nil {
			t.Fatal("expected wrapped insert error")
		}
	})
}

func TestAddCommentEventInTx(t *testing.T) {
	t.Parallel()

	insertQ := regexp.QuoteMeta("INSERT INTO events (id, issue_id, event_type, actor, comment)")

	t.Run("inserts a commented event", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		expectNotWisp(mock, "bd-1")
		mock.ExpectExec(insertQ).
			WithArgs(sqlmock.AnyArg(), "bd-1", "commented", "alice", "a note").
			WillReturnResult(sqlmock.NewResult(1, 1))
		if err := AddCommentEventInTx(context.Background(), tx, "bd-1", "alice", "a note"); err != nil {
			t.Fatalf("AddCommentEventInTx: %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet expectations: %v", err)
		}
	})

	t.Run("insert error is wrapped", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		expectNotWisp(mock, "bd-1")
		mock.ExpectExec(insertQ).WillReturnError(errors.New("boom"))
		if err := AddCommentEventInTx(context.Background(), tx, "bd-1", "alice", "a note"); err == nil {
			t.Fatal("expected wrapped insert error")
		}
	})
}
