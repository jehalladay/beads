package dolt

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	mysql "github.com/go-sql-driver/mysql"
)

// Fault-injection sweep of bd's Dolt write path (beads-pj0).
//
// These tests drive the retry/transaction machinery with sqlmock so we can
// inject connection drops, query timeouts, dirty/aborted transactions, and
// serialization conflicts at EXACT transaction phases (body vs commit) that a
// live server cannot be made to reproduce deterministically. The invariant
// under test is that bd fails CLOSED: it never double-applies a write, never
// silently drops one, and surfaces an actionable error.
//
// The crown-jewel case is the commit-phase double-apply guard in withRetryTx:
// a connection loss during tx.Commit() is AMBIGUOUS (the commit may have
// landed on the server before the socket dropped), so it must NOT be replayed.
// Replaying an already-applied write is silent data corruption.

// newMockStore returns a DoltStore backed by a sqlmock DB plus the mock
// controller. Ordered expectations (the sqlmock default) let each test assert
// the exact sequence of Begin/Exec/Commit/Rollback the code performs.
func newMockStore(t *testing.T) (*DoltStore, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return &DoltStore{db: db}, mock
}

// TestWithRetryTx_CommitPhaseConnectionLoss_NotReplayed is the core safety
// guarantee: when the connection drops DURING commit, the write's fate is
// unknown, so withRetryTx must surface an indeterminate-result error rather
// than replaying the body (which would double-apply if the commit had landed).
func TestWithRetryTx_CommitPhaseConnectionLoss_NotReplayed(t *testing.T) {
	store, mock := newMockStore(t)

	// Exactly ONE begin/exec/commit cycle is modeled. If the code wrongly
	// retries after the commit-phase loss, it will issue a second Begin that
	// has no expectation, which ExpectationsWereMet will flag.
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO issues").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit().WillReturnError(errors.New("write tcp 127.0.0.1:3307: connection reset by peer"))

	bodyCalls := 0
	err := store.withRetryTx(context.Background(), func(tx *sql.Tx) error {
		bodyCalls++
		_, execErr := tx.ExecContext(context.Background(), "INSERT INTO issues (id) VALUES (?)", "x1")
		return execErr
	})

	if err == nil {
		t.Fatal("expected an error after commit-phase connection loss, got nil")
	}
	if bodyCalls != 1 {
		t.Fatalf("body ran %d times; commit-phase loss must NOT be replayed (double-apply risk)", bodyCalls)
	}
	// The error must be the explicit indeterminate-result marker, not a bare
	// connection error, so operators know the write may or may not have landed.
	if !strings.Contains(err.Error(), "indeterminate") {
		t.Errorf("expected 'indeterminate' in error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "double-apply") {
		t.Errorf("expected the double-apply rationale in error, got: %v", err)
	}
	if !errors.Is(err, errCommitPhase) {
		t.Errorf("expected errCommitPhase sentinel in chain, got: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unexpected extra DB operations (a retry leaked through?): %v", err)
	}
}

// TestWithRetryTx_PreCommitConnectionLoss_IsRetried verifies the complement:
// a connection error in the transaction BODY (before commit) is safe to replay
// because nothing was committed. The body must re-run and the second attempt
// commit cleanly, landing the write exactly once.
func TestWithRetryTx_PreCommitConnectionLoss_IsRetried(t *testing.T) {
	store, mock := newMockStore(t)

	// Attempt 1: body exec fails with a transient connection error → rollback.
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO issues").WillReturnError(errors.New("invalid connection"))
	mock.ExpectRollback()
	// Attempt 2: succeeds end to end.
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO issues").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	bodyCalls := 0
	err := store.withRetryTx(context.Background(), func(tx *sql.Tx) error {
		bodyCalls++
		_, execErr := tx.ExecContext(context.Background(), "INSERT INTO issues (id) VALUES (?)", "x1")
		return execErr
	})

	if err != nil {
		t.Fatalf("pre-commit transient error should be retried to success, got: %v", err)
	}
	if bodyCalls != 2 {
		t.Fatalf("expected body to run twice (1 transient failure + 1 success), got %d", bodyCalls)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("expectations not met: %v", err)
	}
}

// TestWithRetryTx_SerializationBodyError_IsRetried verifies that a Dolt
// serialization failure (MySQL 1213 deadlock) in the body — which guarantees a
// server-side rollback — is retried transparently.
func TestWithRetryTx_SerializationBodyError_IsRetried(t *testing.T) {
	store, mock := newMockStore(t)

	mock.ExpectBegin()
	mock.ExpectExec("UPDATE issues").WillReturnError(&mysql.MySQLError{Number: 1213, Message: "Deadlock found when trying to get lock"})
	mock.ExpectRollback()
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE issues").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	bodyCalls := 0
	err := store.withRetryTx(context.Background(), func(tx *sql.Tx) error {
		bodyCalls++
		_, execErr := tx.ExecContext(context.Background(), "UPDATE issues SET status = ?", "closed")
		return execErr
	})

	if err != nil {
		t.Fatalf("serialization error should be retried to success, got: %v", err)
	}
	if bodyCalls != 2 {
		t.Fatalf("expected 2 body calls (1 deadlock + 1 success), got %d", bodyCalls)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("expectations not met: %v", err)
	}
}

// TestWithRetryTx_SerializationCommitError_IsRetried covers the subtle
// interaction that makes the commit-phase guard SAFE: a serialization failure
// (1205 lock-wait-timeout) even at commit time is retried, because 1213/1205
// guarantee the transaction rolled back — so unlike an ambiguous connection
// drop, replaying cannot double-apply. isSerializationError is checked BEFORE
// the errCommitPhase guard precisely to allow this.
func TestWithRetryTx_SerializationCommitError_IsRetried(t *testing.T) {
	store, mock := newMockStore(t)

	// Attempt 1: body ok, commit fails with 1205 (guaranteed rollback).
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO issues").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit().WillReturnError(&mysql.MySQLError{Number: 1205, Message: "Lock wait timeout exceeded; try restarting transaction"})
	// Attempt 2: clean.
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO issues").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	bodyCalls := 0
	err := store.withRetryTx(context.Background(), func(tx *sql.Tx) error {
		bodyCalls++
		_, execErr := tx.ExecContext(context.Background(), "INSERT INTO issues (id) VALUES (?)", "x1")
		return execErr
	})

	if err != nil {
		t.Fatalf("serialization-at-commit should be retried (guaranteed rollback), got: %v", err)
	}
	if bodyCalls != 2 {
		t.Fatalf("expected 2 body calls (1 commit-1205 + 1 success), got %d", bodyCalls)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("expectations not met: %v", err)
	}
}

// TestWithRetryTx_NonRetryableBodyError_FailsFast verifies that a genuine
// application error (e.g. a duplicate-key / constraint violation) is NOT
// retried: bd surfaces it immediately after a single rollback so the caller
// sees the real cause rather than a timed-out retry storm.
func TestWithRetryTx_NonRetryableBodyError_FailsFast(t *testing.T) {
	store, mock := newMockStore(t)

	dupErr := &mysql.MySQLError{Number: 1062, Message: "Duplicate entry 'x1' for key 'PRIMARY'"}
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO issues").WillReturnError(dupErr)
	mock.ExpectRollback()

	bodyCalls := 0
	err := store.withRetryTx(context.Background(), func(tx *sql.Tx) error {
		bodyCalls++
		_, execErr := tx.ExecContext(context.Background(), "INSERT INTO issues (id) VALUES (?)", "x1")
		return execErr
	})

	if err == nil {
		t.Fatal("expected the duplicate-key error to surface, got nil")
	}
	if !errors.Is(err, dupErr) {
		t.Errorf("expected the original MySQL 1062 error in the chain, got: %v", err)
	}
	if bodyCalls != 1 {
		t.Fatalf("non-retryable error must not be retried; body ran %d times", bodyCalls)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("expectations not met: %v", err)
	}
}

// TestWithRetryTx_BeginFailure_ConnectionRefused verifies that a transient
// failure to even open the transaction (server bounce → connection refused) is
// retried, and the body is never invoked while the server is unreachable.
func TestWithRetryTx_BeginFailure_ConnectionRefused(t *testing.T) {
	store, mock := newMockStore(t)

	mock.ExpectBegin().WillReturnError(errors.New("dial tcp 127.0.0.1:3307: connect: connection refused"))
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO issues").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	bodyCalls := 0
	err := store.withRetryTx(context.Background(), func(tx *sql.Tx) error {
		bodyCalls++
		_, execErr := tx.ExecContext(context.Background(), "INSERT INTO issues (id) VALUES (?)", "x1")
		return execErr
	})

	if err != nil {
		t.Fatalf("connection-refused at BeginTx should be retried to success, got: %v", err)
	}
	if bodyCalls != 1 {
		t.Fatalf("body should run exactly once (only after a successful Begin), got %d", bodyCalls)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("expectations not met: %v", err)
	}
}

// TestWithRetryTx_ClosedStore_FailsClosed verifies that a write attempted after
// Close() returns ErrStoreClosed and never touches the (niled) DB — no panic,
// no partial write. This is the use-after-close fault path.
func TestWithRetryTx_ClosedStore_FailsClosed(t *testing.T) {
	store, _ := newMockStore(t)
	store.closed.Store(true)

	bodyCalls := 0
	err := store.withRetryTx(context.Background(), func(tx *sql.Tx) error {
		bodyCalls++
		return nil
	})

	if !errors.Is(err, ErrStoreClosed) {
		t.Fatalf("expected ErrStoreClosed on a closed store, got: %v", err)
	}
	if bodyCalls != 0 {
		t.Fatalf("body must not run on a closed store, ran %d times", bodyCalls)
	}
}

// TestWithWriteTx_BodyError_RollsBack asserts the single-shot write path rolls
// back (does not commit) when the body returns an error, and joins the rollback
// result so a rollback failure is not swallowed.
func TestWithWriteTx_BodyError_RollsBack(t *testing.T) {
	store, mock := newMockStore(t)

	bodyErr := errors.New("validation failed: priority out of range")
	mock.ExpectBegin()
	mock.ExpectRollback()

	err := store.withWriteTx(context.Background(), func(tx *sql.Tx) error {
		return bodyErr
	})

	if !errors.Is(err, bodyErr) {
		t.Fatalf("expected the body error to surface, got: %v", err)
	}
	// Must NOT be tagged as a commit-phase failure — nothing was committed.
	if errors.Is(err, errCommitPhase) {
		t.Error("a body-phase error must not be tagged errCommitPhase (it would defeat the double-apply guard)")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("expected exactly Begin+Rollback (no Commit): %v", err)
	}
}

// TestWithWriteTx_CommitError_TaggedCommitPhase asserts the single-shot write
// path tags a commit failure with errCommitPhase so withRetryTx can tell an
// ambiguous commit loss apart from a safe-to-replay body failure.
func TestWithWriteTx_CommitError_TaggedCommitPhase(t *testing.T) {
	store, mock := newMockStore(t)

	commitErr := errors.New("broken pipe")
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO issues").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit().WillReturnError(commitErr)

	err := store.withWriteTx(context.Background(), func(tx *sql.Tx) error {
		_, execErr := tx.ExecContext(context.Background(), "INSERT INTO issues (id) VALUES (?)", "x1")
		return execErr
	})

	if !errors.Is(err, commitErr) {
		t.Fatalf("expected the commit error in the chain, got: %v", err)
	}
	if !errors.Is(err, errCommitPhase) {
		t.Fatalf("commit failure must be tagged errCommitPhase, got: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("expectations not met: %v", err)
	}
}

// TestIsSerializationError covers the classifier that decides whether a failure
// guarantees a rollback (and is therefore safe to replay at any phase).
func TestIsSerializationError(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"deadlock 1213", &mysql.MySQLError{Number: 1213, Message: "Deadlock found"}, true},
		{"lock wait timeout 1205", &mysql.MySQLError{Number: 1205, Message: "Lock wait timeout exceeded"}, true},
		{"wrapped deadlock", errors.Join(errors.New("commit write tx"), &mysql.MySQLError{Number: 1213}), true},
		{"table not exist 1146", &mysql.MySQLError{Number: 1146, Message: "Table doesn't exist"}, false},
		{"duplicate key 1062", &mysql.MySQLError{Number: 1062, Message: "Duplicate entry"}, false},
		{"plain connection error", errors.New("connection reset by peer"), false},
		{"nil", nil, false},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := isSerializationError(tt.err); got != tt.want {
				t.Errorf("isSerializationError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

// TestIsTableNotExistError covers the classifier used to distinguish a legit
// pre-migration fallthrough (missing optional table) from a real fault. A
// false positive here would make bd treat a corrupt/timed-out read as "table
// absent" and silently skip data — so timeouts/connection errors must NOT
// classify as table-not-exist.
func TestIsTableNotExistError(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"mysql 1146", &mysql.MySQLError{Number: 1146, Message: "Table 'beads.wisps' doesn't exist"}, true},
		{"string error 1146", errors.New("Error 1146: Table doesn't exist"), true},
		{"i/o timeout is not table-absent", errors.New("read tcp: i/o timeout"), false},
		{"connection reset is not table-absent", errors.New("connection reset by peer"), false},
		{"missing column 1054 is not table-absent", &mysql.MySQLError{Number: 1054, Message: "Unknown column 'agent_state'"}, false},
		{"nil", nil, false},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := isTableNotExistError(tt.err); got != tt.want {
				t.Errorf("isTableNotExistError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
