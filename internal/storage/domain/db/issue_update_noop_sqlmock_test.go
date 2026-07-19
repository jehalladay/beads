package db

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"

	"github.com/steveyegge/beads/internal/storage/domain"
)

// beads-j91h: the proxied domain write path (issueSQLRepositoryImpl.Update)
// used to hard-error with sql.ErrNoRows whenever the UPDATE reported
// RowsAffected==0. But RowsAffected==0 conflates two distinct cases: the row
// does not exist, OR the row exists and every SET value already equals its
// current value (a no-op update — MySQL/Dolt report 0 changed rows without
// CLIENT_FOUND_ROWS). The direct store path does not gate on rows==0 at all, so
// a same-value update succeeds there; the proxied path erroring made
// `bd update <id> --status <same-value>` fail on the proxied path (rc=1) where
// the direct path succeeds — a direct-vs-proxied asymmetry.
//
// The fix disambiguates with an existence check on rows==0: a present row means
// a no-op (success, event still recorded); only a truly missing row is
// ErrNoRows. These tests mock the exact SQL branch to prove both legs without a
// live Dolt server (the suite_test harness is Docker-skipped, so a suite test
// would be a false-green).

// updateNoOpSQL is the exact UPDATE emitted for a single non-status field
// (`assignee`) plus the always-appended updated_at column. QueryMatcherEqual
// matches verbatim.
const updateNoOpSQL = "UPDATE issues SET `assignee` = ?, updated_at = ? WHERE id = ?"

// existsSQL is the SELECT the fix issues (via r.Exists) when rows==0.
const existsSQL = "SELECT 1 FROM issues WHERE id = ? LIMIT 1"

// eventInsertSQL is the INSERT the fall-through event Record emits on a
// successful (incl. no-op) update.
const eventInsertSQL = "\n\t\tINSERT INTO events (id, issue_id, event_type, actor, old_value, new_value)\n\t\tVALUES (?, ?, ?, ?, ?, ?)\n\t"

// TestUpdate_NoOpOnExistingRow_Succeeds is the beads-j91h fix: an update that
// changes nothing (RowsAffected==0) on a row that EXISTS must succeed, matching
// the direct path — not hard-error with ErrNoRows.
func TestUpdate_NoOpOnExistingRow_Succeeds(t *testing.T) {
	db, mock := newMockRunner(t)
	repo := &issueSQLRepositoryImpl{runner: db, events: NewEventsSQLRepository(db)}

	// The UPDATE matches the row but changes nothing → 0 rows affected.
	mock.ExpectExec(updateNoOpSQL).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 0))
	// The fix's existence check: the row IS present.
	mock.ExpectQuery(existsSQL).
		WithArgs("bd-1").
		WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow(1))
	// Fall-through: the update event is still recorded (matches direct path).
	mock.ExpectExec(eventInsertSQL).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err := repo.Update(context.Background(), "bd-1", map[string]any{"assignee": "alice"}, "tester", domain.IssueTableOpts{})
	if err != nil {
		t.Fatalf("no-op update on an existing row must succeed (beads-j91h), got: %v", err)
	}
}

// TestUpdate_NoOpOnMissingRow_ReturnsErrNoRows proves the fix does NOT mask a
// genuine missing id: rows==0 AND the existence check finds nothing → ErrNoRows
// (no event recorded).
func TestUpdate_NoOpOnMissingRow_ReturnsErrNoRows(t *testing.T) {
	db, mock := newMockRunner(t)
	repo := &issueSQLRepositoryImpl{runner: db, events: NewEventsSQLRepository(db)}

	mock.ExpectExec(updateNoOpSQL).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 0))
	// Existence check: the row is NOT present (empty result set).
	mock.ExpectQuery(existsSQL).
		WithArgs("bd-missing").
		WillReturnRows(sqlmock.NewRows([]string{"1"}))
	// No event Record expected — a missing row must not record an update event.

	err := repo.Update(context.Background(), "bd-missing", map[string]any{"assignee": "alice"}, "tester", domain.IssueTableOpts{})
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("update on a missing row must return ErrNoRows (beads-j91h guard), got: %v", err)
	}
}
