package issueops

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/steveyegge/beads/internal/types"
)

// TestGetStaleIssuesInTxStatusRouting covers beads-h40fl: `bd stale --status
// blocked` must route to the is_blocked pseudo-status predicate, not to a plain
// `status = 'blocked'` clause (which is unsatisfiable by construction — a
// blocked issue keeps stored status='open'/'in_progress', blocked-ness lives in
// the denormalized is_blocked column). Before the fix the blocked case emitted
// `status = ?` with arg "blocked" → always 0 rows, a silent false negative
// (rc=0 "No stale issues found"). This mirrors the beads-7f3g routing for
// bd list/count --status blocked and bd blocked's closed/pinned exclusion.
//
// The other statuses are real stored values and must still use `status = ?`;
// the empty/default case must keep the open+in_progress IN() clause.
func TestGetStaleIssuesInTxStatusRouting(t *testing.T) {
	t.Parallel()

	// The query builder always SELECTs ids first; returning zero rows exercises
	// the full WHERE clause + args without needing the follow-up batch fetch.
	emptyIDs := func() *sqlmock.Rows { return sqlmock.NewRows([]string{"id"}) }

	t.Run("blocked routes to is_blocked predicate, not status column", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		// Must contain the is_blocked pseudo-status predicate with the
		// closed/pinned exclusion, and must NOT bind a "blocked" status arg.
		mock.ExpectQuery(`is_blocked = 1 AND status <> 'closed' AND status <> 'pinned'`).
			WithArgs(sqlmock.AnyArg()). // only the cutoff timestamp; no status arg
			WillReturnRows(emptyIDs())

		_, err := GetStaleIssuesInTx(context.Background(), tx,
			types.StaleFilter{Days: 1, Status: string(types.StatusBlocked)})
		if err != nil {
			t.Fatalf("GetStaleIssuesInTx(blocked): %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("blocked routing not met (still `status = 'blocked'`?): %v", err)
		}
	})

	t.Run("real stored status uses status = ? with its arg", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery(`status = \?`).
			WithArgs(sqlmock.AnyArg(), "deferred").
			WillReturnRows(emptyIDs())

		_, err := GetStaleIssuesInTx(context.Background(), tx,
			types.StaleFilter{Days: 1, Status: "deferred"})
		if err != nil {
			t.Fatalf("GetStaleIssuesInTx(deferred): %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("deferred should bind `status = ? , deferred`: %v", err)
		}
	})

	t.Run("empty status keeps open+in_progress default", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery(`status IN \('open', 'in_progress'\)`).
			WithArgs(sqlmock.AnyArg()).
			WillReturnRows(emptyIDs())

		_, err := GetStaleIssuesInTx(context.Background(), tx,
			types.StaleFilter{Days: 1})
		if err != nil {
			t.Fatalf("GetStaleIssuesInTx(default): %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("default should keep open+in_progress IN() clause: %v", err)
		}
	})
}
