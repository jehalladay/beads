package uow

import (
	"context"
	"errors"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
)

// TestBeginTx_ClosesConnWhenStartTransactionFails is the beads-lane guard: when
// `START TRANSACTION;` fails after a connection has been pinned via db.Conn(),
// BeginTx must release the connection before returning the error. Otherwise the
// pinned *sql.Conn leaks back to the pool as an in-use connection on every
// failed begin — under a flapping/degraded Dolt server this exhausts the pool
// and wedges all writes.
//
// The true signal of the leak is db.Stats().InUse: a *sql.Conn that is never
// Close()d stays checked out of the pool. After the fix, InUse returns to 0.
func TestBeginTx_ClosesConnWhenStartTransactionFails(t *testing.T) {
	dbConn, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer dbConn.Close()

	mock.ExpectExec("START TRANSACTION;").WillReturnError(errors.New("boom: start transaction failed"))

	p := &doltSQLProvider{defaultBranch: "main", db: dbConn}

	tx, err := p.BeginTx(context.Background())
	if err == nil {
		t.Fatalf("BeginTx: expected error when START TRANSACTION fails, got tx=%v", tx)
	}
	if tx != nil {
		t.Fatalf("BeginTx: expected nil Tx on error, got %v", tx)
	}

	if inUse := dbConn.Stats().InUse; inUse != 0 {
		t.Errorf("BeginTx leaked a pinned connection when START TRANSACTION failed: "+
			"db.Stats().InUse = %d, want 0", inUse)
	}
}
