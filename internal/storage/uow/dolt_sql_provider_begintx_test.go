package uow

import (
	"context"
	"errors"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
)

// TestBeginTx_ReleasesConnOnStartTransactionFailure is a regression test for
// beads-e3rj: when START TRANSACTION fails on an already-pinned connection,
// BeginTx must close that connection so it returns to the pool. The leak is
// proven behaviorally by capping the pool at a single connection: if the failed
// BeginTx leaks its conn, a subsequent acquire can never succeed (the sole
// connection is stuck busy); if it releases correctly, the next BeginTx works.
func TestBeginTx_ReleasesConnOnStartTransactionFailure(t *testing.T) {
	sdb, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(false))
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer sdb.Close()

	// One connection in the pool: a leaked conn makes the pool permanently empty.
	sdb.SetMaxOpenConns(1)

	// First BeginTx: START TRANSACTION fails.
	mock.ExpectExec("START TRANSACTION").WillReturnError(errors.New("server went away"))
	// Second BeginTx: START TRANSACTION succeeds — only reachable if the first
	// call returned its connection to the pool.
	mock.ExpectExec("START TRANSACTION").WillReturnResult(sqlmock.NewResult(0, 0))

	p := &doltSQLProvider{defaultBranch: defaultBranch, db: sdb}

	if _, err := p.BeginTx(context.Background()); err == nil {
		t.Fatal("BeginTx: want error from failed START TRANSACTION, got nil")
	}

	// Bound the second acquire so a leaked-conn pool fails fast instead of
	// hanging the test for the full test timeout.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	tx, err := p.BeginTx(ctx)
	if err != nil {
		t.Fatalf("second BeginTx failed (connection was leaked by the first): %v", err)
	}
	// Release cleanly so the mock's expectations are satisfiable.
	tx.Rollback(context.Background())
}
