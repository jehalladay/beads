package issueops

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

// tableNotExistErr matches the dberrors "table doesn't exist" classifier
// (quoted-table pattern), exercising the optional-table fallback paths.
func tableNotExistErr() error {
	return errors.New("Error 1146: Table 'beads.wisps' doesn't exist")
}

func TestWispsTableEmptyOrMissingInTx(t *testing.T) {
	t.Parallel()

	probe := regexp.QuoteMeta("SELECT 1 FROM wisps LIMIT 1")

	t.Run("row present -> not empty", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery(probe).WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow(1))
		empty, err := wispsTableEmptyOrMissingInTx(context.Background(), tx)
		if err != nil || empty {
			t.Fatalf("got (empty=%v,err=%v), want (false,nil)", empty, err)
		}
	})

	t.Run("no rows -> empty", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery(probe).WillReturnError(sql.ErrNoRows)
		empty, err := wispsTableEmptyOrMissingInTx(context.Background(), tx)
		if err != nil || !empty {
			t.Fatalf("got (empty=%v,err=%v), want (true,nil)", empty, err)
		}
	})

	t.Run("table missing -> empty", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery(probe).WillReturnError(tableNotExistErr())
		empty, err := wispsTableEmptyOrMissingInTx(context.Background(), tx)
		if err != nil || !empty {
			t.Fatalf("got (empty=%v,err=%v), want (true,nil)", empty, err)
		}
	})

	t.Run("other error propagates", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery(probe).WillReturnError(errors.New("boom"))
		if _, err := wispsTableEmptyOrMissingInTx(context.Background(), tx); err == nil {
			t.Fatal("expected error to propagate")
		}
	})
}

func TestPartitionWispIDsInTx(t *testing.T) {
	t.Parallel()

	probe := regexp.QuoteMeta("SELECT 1 FROM wisps LIMIT 1")
	inQ := regexp.QuoteMeta("SELECT id FROM wisps WHERE id IN (?,?)")

	t.Run("empty ids -> all nil", func(t *testing.T) {
		t.Parallel()
		_, _, tx := beginMockTx(t)
		w, p, err := PartitionWispIDsInTx(context.Background(), tx, nil)
		if err != nil || w != nil || p != nil {
			t.Fatalf("got (%v,%v,%v), want (nil,nil,nil)", w, p, err)
		}
	})

	t.Run("wisps table empty -> all perm", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery(probe).WillReturnError(sql.ErrNoRows)
		w, p, err := PartitionWispIDsInTx(context.Background(), tx, []string{"bd-1", "bd-2"})
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if len(w) != 0 || len(p) != 2 {
			t.Fatalf("got wisp=%v perm=%v, want all perm", w, p)
		}
	})

	t.Run("splits ids by wisp membership", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery(probe).WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow(1))
		mock.ExpectQuery(inQ).WithArgs("bd-1", "bd-2").
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("bd-1"))
		w, p, err := PartitionWispIDsInTx(context.Background(), tx, []string{"bd-1", "bd-2"})
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if len(w) != 1 || w[0] != "bd-1" {
			t.Fatalf("wispIDs = %v, want [bd-1]", w)
		}
		if len(p) != 1 || p[0] != "bd-2" {
			t.Fatalf("permIDs = %v, want [bd-2]", p)
		}
	})

	t.Run("wisps IN-query table-missing mid-flight -> all perm", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery(probe).WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow(1))
		mock.ExpectQuery(inQ).WithArgs("bd-1", "bd-2").WillReturnError(tableNotExistErr())
		w, p, err := PartitionWispIDsInTx(context.Background(), tx, []string{"bd-1", "bd-2"})
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if len(w) != 0 || len(p) != 2 {
			t.Fatalf("got wisp=%v perm=%v, want all perm on table-missing", w, p)
		}
	})
}

func TestGetCommentCountsInTx(t *testing.T) {
	t.Parallel()

	probe := regexp.QuoteMeta("SELECT 1 FROM wisps LIMIT 1")

	t.Run("empty ids -> empty map", func(t *testing.T) {
		t.Parallel()
		_, _, tx := beginMockTx(t)
		got, err := GetCommentCountsInTx(context.Background(), tx, nil)
		if err != nil || len(got) != 0 {
			t.Fatalf("got (%v,%v), want (empty,nil)", got, err)
		}
	})

	t.Run("counts from the regular comments table when no wisps", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery(probe).WillReturnError(sql.ErrNoRows) // wisps empty -> all perm
		mock.ExpectQuery(regexp.QuoteMeta(
			"SELECT issue_id, COUNT(*) as cnt FROM comments WHERE issue_id IN (?,?) GROUP BY issue_id")).
			WithArgs("bd-1", "bd-2").
			WillReturnRows(sqlmock.NewRows([]string{"issue_id", "cnt"}).
				AddRow("bd-1", 3).
				AddRow("bd-2", 1))
		got, err := GetCommentCountsInTx(context.Background(), tx, []string{"bd-1", "bd-2"})
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if got["bd-1"] != 3 || got["bd-2"] != 1 {
			t.Fatalf("counts = %v, want {bd-1:3, bd-2:1}", got)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet expectations: %v", err)
		}
	})

	t.Run("count query error is wrapped", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery(probe).WillReturnError(sql.ErrNoRows)
		mock.ExpectQuery("SELECT issue_id, COUNT").WithArgs("bd-1").WillReturnError(errors.New("boom"))
		if _, err := GetCommentCountsInTx(context.Background(), tx, []string{"bd-1"}); err == nil {
			t.Fatal("expected wrapped count-query error")
		}
	})
}
