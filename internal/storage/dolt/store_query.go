// Package dolt implements the storage interface using Dolt (versioned MySQL-compatible database).
//
// Dolt provides native version control for SQL data with cell-level merge, history queries,
// and federation via Dolt remotes. The database itself is version-controlled.
//
// Dolt capabilities:
//   - Native version control (commit, push, pull, branch, merge)
//   - Time-travel queries via AS OF and dolt_history_* tables
//   - Cell-level merge for conflict resolution
//   - Multi-writer via dolt sql-server (federation, pure Go)
//
// All operations require a running dolt sql-server. Connect via MySQL protocol (pure Go).
package dolt

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/cenkalti/backoff/v4"
	mysql "github.com/go-sql-driver/mysql"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// cliExecTimeout is the maximum time to wait for dolt CLI push/pull operations.
// SSH transfers can hang indefinitely on network issues or SSH key prompts;
// this prevents the process from blocking forever.
const cliExecTimeout = 5 * time.Minute

func withCLIExecTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, cliExecTimeout)
}

// execContext wraps a write statement in an explicit BEGIN/COMMIT to ensure
// durability when the Dolt server runs with autocommit disabled (the default
// when started with --no-auto-commit). Without this, writes remain in an
// ErrStoreClosed is returned when an operation is attempted on a closed store.
var ErrStoreClosed = errors.New("store is closed")

// rlockOpen acquires the read lock and verifies the store is still open. On
// success it returns a release func (which the caller MUST defer) and nil; on a
// closed store it releases the lock and returns ErrStoreClosed.
//
// Checking s.closed/s.db *while holding s.mu* is what closes the TOCTOU against
// Close. Close sets s.closed=true and only then takes s.mu.Lock() before niling
// s.db, so a bare `if s.closed.Load()` check before the lock left a window where
// Close could nil s.db between the check and the first s.db.* call, panicking
// with a nil-pointer deref (use-after-close). Holding the read lock across the
// whole db access forces Close's write-lock to wait, and re-reading closed/db
// under the lock guarantees we never touch a niled s.db.
func (s *DoltStore) rlockOpen() (func(), error) {
	s.mu.RLock()
	if s.closed.Load() || s.db == nil {
		s.mu.RUnlock()
		return nil, ErrStoreClosed
	}
	return s.mu.RUnlock, nil
}

// withReadTx runs fn inside a transaction while holding the store's read-lock.
// Used for read operations that need a *sql.Tx to share issueops functions.
//
// The whole BeginTx+fn is wrapped in withRetry so a transient connection error
// (e.g. "invalid connection" when the dolt sql-server reaps a pooled connection
// that has been idle past its wait_timeout) is retried rather than surfaced to
// the caller. This is safe because fn is read-only and the transaction is always
// rolled back, so re-running the operation has no side effects.
func (s *DoltStore) withReadTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	release, err := s.rlockOpen()
	if err != nil {
		return err
	}
	defer release()
	return s.withRetry(ctx, func() error {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin read tx: %w", err)
		}
		defer func() { _ = tx.Rollback() }()
		return fn(tx)
	})
}

func (s *DoltStore) withRetryTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	bo := backoff.NewExponentialBackOff()
	bo.InitialInterval = 25 * time.Millisecond
	bo.MaxElapsedTime = 5 * time.Second
	if s.serverMode {
		bo.MaxElapsedTime = 15 * time.Second
	}
	return backoff.Retry(func() error {
		err := s.withWriteTx(ctx, fn)
		if err == nil {
			return nil
		}
		// Serialization failures (1213/1205) guarantee a server-side rollback,
		// so the write never landed — safe to replay at any phase.
		if isSerializationError(err) {
			doltMetrics.serializationErrors.Add(ctx, 1)
			doltMetrics.writeRetries.Add(ctx, 1, metric.WithAttributes(attribute.String("type", "serialization")))
			return err // retryable
		}
		// Connection failures are only safe to replay BEFORE commit (BeginTx or
		// the body): nothing was committed. A failure tagged errCommitPhase is
		// ambiguous — the commit may have landed before the connection dropped —
		// so replaying could double-apply the write. Surface it instead.
		if isRetryableError(err) {
			if errors.Is(err, errCommitPhase) {
				return backoff.Permanent(fmt.Errorf("write commit result indeterminate after connection loss (not retried to avoid double-apply): %w", err))
			}
			doltMetrics.writeRetries.Add(ctx, 1, metric.WithAttributes(attribute.String("type", "connection")))
			return err // pre-commit transient: retryable
		}
		return backoff.Permanent(err)
	}, backoff.WithContext(bo, ctx))
}

func (s *DoltStore) withWriteTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	release, err := s.rlockOpen()
	if err != nil {
		return err
	}
	defer release()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin write tx: %w", err)
	}
	if err := fn(tx); err != nil {
		return errors.Join(err, tx.Rollback())
	}
	if err := tx.Commit(); err != nil {
		// Tag commit-phase failures so withRetryTx can tell an ambiguous commit
		// loss apart from a safe-to-replay pre-commit failure.
		return fmt.Errorf("commit write tx: %w (%w)", err, errCommitPhase)
	}
	return nil
}

// uncommitted implicit transaction that Dolt rolls back on connection close,
// causing silent data loss for callers that do not use db.BeginTx themselves.
func (s *DoltStore) execContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	release, err := s.rlockOpen()
	if err != nil {
		return nil, err
	}
	defer release()
	ctx, span := doltTracer.Start(ctx, "dolt.exec",
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(append(s.doltSpanAttrs(),
			attribute.String("db.operation", "exec"),
			attribute.String("db.statement", spanSQL(query)),
		)...),
	)
	var result sql.Result
	err = s.withRetry(ctx, func() error {
		tx, txErr := s.db.BeginTx(ctx, nil)
		if txErr != nil {
			return txErr
		}
		var execErr error
		result, execErr = tx.ExecContext(ctx, query, args...)
		if execErr != nil {
			_ = tx.Rollback()
			return execErr
		}
		return tx.Commit()
	})
	finalErr := wrapLockError(err)
	endSpan(span, finalErr)
	return result, finalErr
}

// QueryContext wraps s.db.QueryContext with retry for transient errors.
// Exported so callers (e.g. backup) can run ad-hoc queries with retry
// instead of going through the raw *sql.DB.
func (s *DoltStore) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return s.queryContext(ctx, query, args...)
}

// queryContext wraps s.db.QueryContext with retry for transient errors.
func (s *DoltStore) queryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	release, err := s.rlockOpen()
	if err != nil {
		return nil, err
	}
	defer release()
	ctx, span := doltTracer.Start(ctx, "dolt.query",
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(append(s.doltSpanAttrs(),
			attribute.String("db.operation", "query"),
			attribute.String("db.statement", spanSQL(query)),
		)...),
	)
	var rows *sql.Rows
	err = s.withRetry(ctx, func() error {
		// Close any Rows from a previous failed attempt to avoid leaking connections.
		if rows != nil {
			_ = rows.Close()
			rows = nil
		}
		var queryErr error
		//nolint:rowserrcheck // this is a retry-wrapper that RETURNS rows to the
		// caller; rows.Err() is the caller's responsibility after iteration and
		// cannot be checked here without consuming the result set.
		rows, queryErr = s.db.QueryContext(ctx, query, args...)
		return queryErr
	})
	finalErr := wrapLockError(err)
	endSpan(span, finalErr)
	return rows, finalErr
}

// queryRowContext wraps s.db.QueryRowContext with retry for transient errors.
// The scan function receives the *sql.Row and should call .Scan() on it.
func (s *DoltStore) queryRowContext(ctx context.Context, scan func(*sql.Row) error, query string, args ...any) error {
	release, err := s.rlockOpen()
	if err != nil {
		return err
	}
	defer release()
	ctx, span := doltTracer.Start(ctx, "dolt.query_row",
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(append(s.doltSpanAttrs(),
			attribute.String("db.operation", "query_row"),
			attribute.String("db.statement", spanSQL(query)),
		)...),
	)
	finalErr := wrapLockError(s.withRetry(ctx, func() error {
		row := s.db.QueryRowContext(ctx, query, args...)
		return scan(row)
	}))
	endSpan(span, finalErr)
	return finalErr
}

// execWithLongTimeout opens a one-shot database connection with readTimeout=5m
// and executes the given query. Push/pull operations can exceed the default
// readTimeout when the server performs network I/O to git remotes.
//
// The query is wrapped in an explicit transaction (BEGIN/COMMIT) so that
// DOLT_PULL merge operations succeed even when the server runs with
// autocommit=1. Without this, Dolt rejects merges under autocommit because
// it cannot expose conflict-resolution tables to the caller.
func (s *DoltStore) execWithLongTimeout(ctx context.Context, query string, args ...any) error {
	cfg, err := mysql.ParseDSN(s.connStr)
	if err != nil {
		return fmt.Errorf("failed to parse DSN for long-timeout connection: %w", err)
	}
	cfg.ReadTimeout = 5 * time.Minute
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		return fmt.Errorf("failed to open long-timeout connection: %w", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	if _, err := tx.ExecContext(ctx, query, args...); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// execWithLongTimeoutNoTx executes a long-running Dolt stored procedure without
// an explicit transaction. Push operations do not need the pull/merge conflict
// handling above, and DOLT_PUSH has diverged from direct `dolt push` behavior
// when wrapped in a SQL transaction.
func (s *DoltStore) execWithLongTimeoutNoTx(ctx context.Context, query string, args ...any) error {
	cfg, err := mysql.ParseDSN(s.connStr)
	if err != nil {
		return fmt.Errorf("failed to parse DSN for long-timeout connection: %w", err)
	}
	cfg.ReadTimeout = 5 * time.Minute
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		return fmt.Errorf("failed to open long-timeout connection: %w", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	_, err = db.ExecContext(ctx, query, args...)
	return err
}
