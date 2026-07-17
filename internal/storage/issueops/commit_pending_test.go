package issueops

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

// (IsNothingToCommitError is exercised separately in graph_slice_helpers_test.go.)

// These tests cover the commit_pending.go helpers (HasPendingChanges and
// BuildBatchCommitMessage) using sqlmock — hermetic, no live Dolt. Both take
// the SQLQuerier interface, which a bare *sql.DB satisfies, so no transaction
// wrapper is needed. The default sqlmock QueryMatcher is regexp/partial, so
// query expectations match on stable substrings rather than the full SQL text.

// newMockQuerier returns a sqlmock-backed *sql.DB (usable directly as a
// SQLQuerier) plus its mock controller.
func newMockQuerier(t *testing.T) (*sql.DB, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, mock
}

// TestHasPendingChanges_Positive covers the count>0 branch (changes present).
func TestHasPendingChanges_Positive(t *testing.T) {
	t.Parallel()
	db, mock := newMockQuerier(t)

	mock.ExpectQuery(`FROM dolt_status`).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(3))

	pending, err := HasPendingChanges(context.Background(), db)
	if err != nil {
		t.Fatalf("HasPendingChanges: %v", err)
	}
	if !pending {
		t.Errorf("pending = false, want true (count 3 > 0)")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

// TestHasPendingChanges_Empty covers the count==0 branch (nothing to commit).
func TestHasPendingChanges_Empty(t *testing.T) {
	t.Parallel()
	db, mock := newMockQuerier(t)

	mock.ExpectQuery(`FROM dolt_status`).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))

	pending, err := HasPendingChanges(context.Background(), db)
	if err != nil {
		t.Fatalf("HasPendingChanges: %v", err)
	}
	if pending {
		t.Errorf("pending = true, want false (count 0)")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

// TestHasPendingChanges_QueryError covers the error-wrapping path when the
// status query fails.
func TestHasPendingChanges_QueryError(t *testing.T) {
	t.Parallel()
	db, mock := newMockQuerier(t)

	mock.ExpectQuery(`FROM dolt_status`).
		WillReturnError(errors.New("boom"))

	_, err := HasPendingChanges(context.Background(), db)
	if err == nil {
		t.Fatal("HasPendingChanges err = nil, want an error")
	}
	if !strings.Contains(err.Error(), "failed to check status") {
		t.Errorf("err = %q, want it wrapped with 'failed to check status'", err)
	}
}

// TestBuildBatchCommitMessage_Full covers the happy path: an explicit actor,
// all three diff-type counts, and a non-empty other-tables suffix.
func TestBuildBatchCommitMessage_Full(t *testing.T) {
	t.Parallel()
	db, mock := newMockQuerier(t)

	mock.ExpectQuery(`dolt_diff`).
		WillReturnRows(sqlmock.NewRows([]string{"diff_type", "cnt"}).
			AddRow("added", 2).
			AddRow("modified", 5).
			AddRow("removed", 1))
	mock.ExpectQuery(`FROM dolt_status`).
		WillReturnRows(sqlmock.NewRows([]string{"table_name"}).
			AddRow("labels").
			AddRow("comments"))

	msg := BuildBatchCommitMessage(context.Background(), db, "alice")

	for _, want := range []string{
		"bd: batch commit by alice",
		"2 created", "5 updated", "1 deleted",
		"(+ labels, comments)",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("msg = %q, missing %q", msg, want)
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

// TestBuildBatchCommitMessage_DefaultActorNoChanges covers the empty-actor
// default ("bd") and the no-counts / no-other-tables path (bare message).
func TestBuildBatchCommitMessage_DefaultActorNoChanges(t *testing.T) {
	t.Parallel()
	db, mock := newMockQuerier(t)

	// No diff rows and no other-table rows → bare message, default actor.
	mock.ExpectQuery(`dolt_diff`).
		WillReturnRows(sqlmock.NewRows([]string{"diff_type", "cnt"}))
	mock.ExpectQuery(`FROM dolt_status`).
		WillReturnRows(sqlmock.NewRows([]string{"table_name"}))

	msg := BuildBatchCommitMessage(context.Background(), db, "")

	if msg != "bd: batch commit by bd" {
		t.Errorf("msg = %q, want %q", msg, "bd: batch commit by bd")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

// TestBuildBatchCommitMessage_QueryErrorsGraceful covers the two graceful
// query-error fall-throughs: when both the dolt_diff and the dolt_status
// queries error, the function still returns the bare message rather than
// failing (err is swallowed by design).
func TestBuildBatchCommitMessage_QueryErrorsGraceful(t *testing.T) {
	t.Parallel()
	db, mock := newMockQuerier(t)

	mock.ExpectQuery(`dolt_diff`).
		WillReturnError(errors.New("diff failed"))
	mock.ExpectQuery(`FROM dolt_status`).
		WillReturnError(errors.New("status failed"))

	msg := BuildBatchCommitMessage(context.Background(), db, "bob")

	if msg != "bd: batch commit by bob" {
		t.Errorf("msg = %q, want bare %q on query errors", msg, "bd: batch commit by bob")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}
