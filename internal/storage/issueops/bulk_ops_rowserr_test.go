package issueops

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

// Regression guard for beads-r06.15 (audit findings #11/#12): destructive and
// mutating row-iteration paths must propagate a mid-iteration rows.Err().
//
// Without the rows.Err() check, a driver error raised AFTER the last successful
// row (e.g. a dropped connection mid-scan) is silently swallowed: the ID slice
// is truncated and the destructive operation proceeds against a PARTIAL set
// while reporting success. For a delete-by-source-repo path that means the
// affected/recompute bookkeeping is computed from the wrong set.

// errMidScan is the injected driver error surfaced by rows.Err() after the
// final row has been yielded.
var errMidScan = errors.New("simulated driver error mid-iteration")

// TestDeleteIssuesBySourceRepoInTxPropagatesRowsErr asserts that when the
// initial `SELECT id FROM issues WHERE source_repo = ?` scan loop hits a
// row-iteration error, DeleteIssuesBySourceRepoInTx returns that error instead
// of silently proceeding to DELETE with a truncated ID set.
func TestDeleteIssuesBySourceRepoInTxPropagatesRowsErr(t *testing.T) {
	t.Parallel()

	_, mock, tx := beginMockTx(t)

	// One row is yielded, then the driver reports an error via rows.Err().
	rows := sqlmock.NewRows([]string{"id"}).
		AddRow("issue-1").
		RowError(0, errMidScan)
	mock.ExpectQuery(`SELECT id FROM issues WHERE source_repo = \?`).
		WithArgs("acme/widgets").
		WillReturnRows(rows)

	// No DELETE expectation is registered: if the function ignores rows.Err()
	// and proceeds, sqlmock's ExpectationsWereMet / unexpected-exec will flag
	// the unmodeled DELETE — but the primary assertion is the returned error.

	_, err := DeleteIssuesBySourceRepoInTx(context.Background(), tx, "acme/widgets")
	if err == nil {
		t.Fatal("DeleteIssuesBySourceRepoInTx returned nil error; expected the mid-iteration rows.Err() to propagate (destructive path must not proceed on a truncated ID set)")
	}
	if !errors.Is(err, errMidScan) {
		t.Fatalf("DeleteIssuesBySourceRepoInTx error = %v; want it to wrap %v", err, errMidScan)
	}
}
