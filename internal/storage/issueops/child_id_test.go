package issueops

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

// These tests cover GetNextChildIDTx using sqlmock — hermetic, no live Dolt.
// The function issues, in order: an IsActiveWispInTx probe (SELECT 1 FROM
// wisps), a counter read (SELECT last_child FROM <counter table>), an existing-
// children scan (SELECT id FROM <issue table> WHERE id LIKE ...), and a counter
// upsert (INSERT ... ON DUPLICATE KEY UPDATE). The default sqlmock QueryMatcher
// is regexp/partial, so expectations match on stable substrings.

// expectNotWisp (the IsActiveWispInTx probe returning no row, keeping the
// issue-table path) is shared from comments_sqlmock_test.go in this package.

// expectIsWisp queues the IsActiveWispInTx probe returning a row, so the
// function routes to the wisp tables (wisp_child_counters / wisps).
func expectIsWisp(mock sqlmock.Sqlmock, parentID string) {
	mock.ExpectQuery(`SELECT 1 FROM wisps WHERE id = \? LIMIT 1`).
		WithArgs(parentID).
		WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow(1))
}

// TestGetNextChildIDTx_IssuePathFresh: no counter row, no existing children →
// first child is parent.1.
func TestGetNextChildIDTx_IssuePathFresh(t *testing.T) {
	t.Parallel()
	_, mock, tx := beginMockTx(t)

	expectNotWisp(mock, "bd-1")
	// Counter read: no row → lastChild stays 0.
	mock.ExpectQuery(`SELECT last_child FROM child_counters WHERE parent_id = \?`).
		WithArgs("bd-1").
		WillReturnError(sql.ErrNoRows)
	// Existing-children scan: none.
	mock.ExpectQuery(`SELECT id FROM issues`).
		WithArgs("bd-1", "bd-1").
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	// Counter upsert to 1.
	mock.ExpectExec(`INSERT INTO child_counters`).
		WithArgs("bd-1", 1, 1).
		WillReturnResult(sqlmock.NewResult(1, 1))

	got, err := GetNextChildIDTx(context.Background(), tx, "bd-1")
	if err != nil {
		t.Fatalf("GetNextChildIDTx: %v", err)
	}
	if got != "bd-1.1" {
		t.Errorf("got %q, want bd-1.1", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

// TestGetNextChildIDTx_ExistingChildrenWin: the max existing child number beats
// the stored counter, so the next ID is max+1.
func TestGetNextChildIDTx_ExistingChildrenWin(t *testing.T) {
	t.Parallel()
	_, mock, tx := beginMockTx(t)

	expectNotWisp(mock, "bd-2")
	// Counter says 3 ...
	mock.ExpectQuery(`SELECT last_child FROM child_counters WHERE parent_id = \?`).
		WithArgs("bd-2").
		WillReturnRows(sqlmock.NewRows([]string{"last_child"}).AddRow(3))
	// ... but an existing child bd-2.7 is higher (and a malformed row is ignored).
	mock.ExpectQuery(`SELECT id FROM issues`).
		WithArgs("bd-2", "bd-2").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).
			AddRow("bd-2.5").
			AddRow("bd-2.7").
			AddRow("bd-2.notanum"))
	// Upsert to 8 (7 + 1).
	mock.ExpectExec(`INSERT INTO child_counters`).
		WithArgs("bd-2", 8, 8).
		WillReturnResult(sqlmock.NewResult(1, 1))

	got, err := GetNextChildIDTx(context.Background(), tx, "bd-2")
	if err != nil {
		t.Fatalf("GetNextChildIDTx: %v", err)
	}
	if got != "bd-2.8" {
		t.Errorf("got %q, want bd-2.8", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

// TestGetNextChildIDTx_WispPath: an active wisp parent routes to the
// wisp_child_counters / wisps tables.
func TestGetNextChildIDTx_WispPath(t *testing.T) {
	t.Parallel()
	_, mock, tx := beginMockTx(t)

	expectIsWisp(mock, "bd-w")
	mock.ExpectQuery(`SELECT last_child FROM wisp_child_counters WHERE parent_id = \?`).
		WithArgs("bd-w").
		WillReturnRows(sqlmock.NewRows([]string{"last_child"}).AddRow(1))
	mock.ExpectQuery(`SELECT id FROM wisps`).
		WithArgs("bd-w", "bd-w").
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectExec(`INSERT INTO wisp_child_counters`).
		WithArgs("bd-w", 2, 2).
		WillReturnResult(sqlmock.NewResult(1, 1))

	got, err := GetNextChildIDTx(context.Background(), tx, "bd-w")
	if err != nil {
		t.Fatalf("GetNextChildIDTx: %v", err)
	}
	if got != "bd-w.2" {
		t.Errorf("got %q, want bd-w.2", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

// TestGetNextChildIDTx_CounterReadError: a non-ErrNoRows failure reading the
// counter is wrapped and returned.
func TestGetNextChildIDTx_CounterReadError(t *testing.T) {
	t.Parallel()
	_, mock, tx := beginMockTx(t)

	expectNotWisp(mock, "bd-3")
	mock.ExpectQuery(`SELECT last_child FROM child_counters WHERE parent_id = \?`).
		WithArgs("bd-3").
		WillReturnError(errors.New("counter boom"))

	_, err := GetNextChildIDTx(context.Background(), tx, "bd-3")
	if err == nil {
		t.Fatal("err = nil, want counter-read error")
	}
}

// TestGetNextChildIDTx_ChildrenQueryError: the existing-children query failing
// is wrapped and returned.
func TestGetNextChildIDTx_ChildrenQueryError(t *testing.T) {
	t.Parallel()
	_, mock, tx := beginMockTx(t)

	expectNotWisp(mock, "bd-4")
	mock.ExpectQuery(`SELECT last_child FROM child_counters WHERE parent_id = \?`).
		WithArgs("bd-4").
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(`SELECT id FROM issues`).
		WithArgs("bd-4", "bd-4").
		WillReturnError(errors.New("children boom"))

	_, err := GetNextChildIDTx(context.Background(), tx, "bd-4")
	if err == nil {
		t.Fatal("err = nil, want children-query error")
	}
}

// TestGetNextChildIDTx_RowsIterError: an error surfaced during row iteration
// (rows.Err) after the children query succeeds is wrapped and returned.
func TestGetNextChildIDTx_RowsIterError(t *testing.T) {
	t.Parallel()
	_, mock, tx := beginMockTx(t)

	expectNotWisp(mock, "bd-6")
	mock.ExpectQuery(`SELECT last_child FROM child_counters WHERE parent_id = \?`).
		WithArgs("bd-6").
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(`SELECT id FROM issues`).
		WithArgs("bd-6", "bd-6").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).
			AddRow("bd-6.1").
			RowError(0, errors.New("iter boom")))

	_, err := GetNextChildIDTx(context.Background(), tx, "bd-6")
	if err == nil {
		t.Fatal("err = nil, want rows-iteration error")
	}
}

// TestGetNextChildIDTx_ScanError: a child row whose value can't scan into a
// string triggers the per-row scan-error path.
func TestGetNextChildIDTx_ScanError(t *testing.T) {
	t.Parallel()
	_, mock, tx := beginMockTx(t)

	expectNotWisp(mock, "bd-7")
	mock.ExpectQuery(`SELECT last_child FROM child_counters WHERE parent_id = \?`).
		WithArgs("bd-7").
		WillReturnError(sql.ErrNoRows)
	// A nil id value cannot scan into a non-pointer string → Scan error.
	mock.ExpectQuery(`SELECT id FROM issues`).
		WithArgs("bd-7", "bd-7").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(nil))

	_, err := GetNextChildIDTx(context.Background(), tx, "bd-7")
	if err == nil {
		t.Fatal("err = nil, want per-row scan error")
	}
}

// TestGetNextChildIDTx_InsertError: the counter upsert failing is wrapped and
// returned.
func TestGetNextChildIDTx_InsertError(t *testing.T) {
	t.Parallel()
	_, mock, tx := beginMockTx(t)

	expectNotWisp(mock, "bd-5")
	mock.ExpectQuery(`SELECT last_child FROM child_counters WHERE parent_id = \?`).
		WithArgs("bd-5").
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(`SELECT id FROM issues`).
		WithArgs("bd-5", "bd-5").
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectExec(`INSERT INTO child_counters`).
		WithArgs("bd-5", 1, 1).
		WillReturnError(errors.New("insert boom"))

	_, err := GetNextChildIDTx(context.Background(), tx, "bd-5")
	if err == nil {
		t.Fatal("err = nil, want counter-upsert error")
	}
}
