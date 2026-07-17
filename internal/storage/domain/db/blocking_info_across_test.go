package db

import (
	"context"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/go-sql-driver/mysql"
)

// TestGetBlockingInfoAcrossIssuesAndWispsWispTableMissing covers the graceful
// degradation branch: when the wisps-side GetBlockingInfo fails because the
// wisp_dependencies table does not exist (error 1146), the permanent-table
// result must be returned as-is instead of propagating the error. This is the
// live behavior on a store that has never created wisp tables.
func TestGetBlockingInfoAcrossIssuesAndWispsWispTableMissing(t *testing.T) {
	t.Parallel()

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()
	mock.MatchExpectationsInOrder(false)

	repo := NewDependencySQLRepository(db)

	// Permanent-table pass: outbound + inbound blocking queries both return no
	// rows, so no status lookup runs and the perm BlockingInfo is empty-but-init.
	mock.ExpectQuery("FROM dependencies WHERE issue_id IN").
		WillReturnRows(sqlmock.NewRows([]string{"issue_id", "depends_on_id", "type"}))
	mock.ExpectQuery("FROM dependencies WHERE").
		WillReturnRows(sqlmock.NewRows([]string{"issue_id", "depends_on_id", "type"}))

	// Wisp-table pass: the first (outbound) query fails with MySQL 1146
	// (table doesn't exist), which IsTableNotExist must recognize.
	mock.ExpectQuery("FROM wisp_dependencies WHERE").
		WillReturnError(&mysql.MySQLError{
			Number:  1146,
			Message: "Table 'beads.wisp_dependencies' doesn't exist",
		})

	info, err := repo.GetBlockingInfoAcrossIssuesAndWisps(context.Background(), []string{"bd-1"})
	if err != nil {
		t.Fatalf("GetBlockingInfoAcrossIssuesAndWisps: unexpected error %v (wisp table-missing must degrade gracefully)", err)
	}
	// Maps must be non-nil (initialized by the perm pass) and empty.
	if info.BlockedBy == nil || info.Blocks == nil || info.Parent == nil {
		t.Fatalf("BlockingInfo maps must be initialized: %+v", info)
	}
	if len(info.BlockedBy) != 0 || len(info.Blocks) != 0 || len(info.Parent) != 0 {
		t.Errorf("expected empty blocking info, got %+v", info)
	}
}

// TestGetBlockingInfoAcrossIssuesAndWispsPermErrorPropagates verifies the
// non-degradation path: a failure on the PERMANENT-table pass is a real error
// and must propagate (not be swallowed like the wisp table-missing case).
func TestGetBlockingInfoAcrossIssuesAndWispsPermErrorPropagates(t *testing.T) {
	t.Parallel()

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	repo := NewDependencySQLRepository(db)

	mock.ExpectQuery("FROM dependencies WHERE issue_id IN").
		WillReturnError(&mysql.MySQLError{Number: 1064, Message: "syntax error"})

	if _, err := repo.GetBlockingInfoAcrossIssuesAndWisps(context.Background(), []string{"bd-1"}); err == nil {
		t.Fatal("expected the permanent-table error to propagate, got nil")
	}
}
