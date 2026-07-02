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
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/cenkalti/backoff/v4"

	"github.com/steveyegge/beads/internal/storage/schema"
)

// Retry configuration for transient connection errors (stale pool connections,
// brief network issues, server restarts).
const serverRetryMaxElapsed = 30 * time.Second

func newServerRetryBackoff() backoff.BackOff {
	bo := backoff.NewExponentialBackOff()
	bo.MaxElapsedTime = serverRetryMaxElapsed
	return bo
}

// isRetryableError returns true if the error is a transient connection error
// that should be retried in server mode.
func isRetryableError(err error) bool {
	if err == nil {
		return false
	}
	if schema.IsMigrationLockError(err) {
		return true
	}
	errStr := strings.ToLower(err.Error())
	// MySQL driver transient errors
	if strings.Contains(errStr, "driver: bad connection") {
		return true
	}
	if strings.Contains(errStr, "invalid connection") {
		return true
	}
	// Network transient errors (brief blips, not persistent failures)
	if strings.Contains(errStr, "broken pipe") {
		return true
	}
	if strings.Contains(errStr, "connection reset") {
		return true
	}
	// Server restart: "connection refused" is transient — the server may
	// come back within the backoff window (30s). Retrying here prevents
	// a brief server outage from cascading into permanent failures.
	if strings.Contains(errStr, "connection refused") {
		return true
	}
	// Dolt read-only mode: under load, Dolt may enter read-only mode with
	// "cannot update manifest: database is read only". This clears after
	// a server restart, so it's worth retrying.
	if strings.Contains(errStr, "database is read only") {
		return true
	}
	// MySQL error 2013: mid-query disconnect
	if strings.Contains(errStr, "lost connection") {
		return true
	}
	// MySQL error 2006: idle connection timeout
	if strings.Contains(errStr, "gone away") {
		return true
	}
	// Go net package timeout on read/write
	if strings.Contains(errStr, "i/o timeout") {
		return true
	}
	// Dolt server catalog race: after CREATE DATABASE, the server's in-memory
	// catalog may not have registered the new database yet. The immediately
	// following USE (implicit via DSN) fails with "Unknown database". This is
	// transient and resolves once the catalog refreshes. (GH-1851)
	if strings.Contains(errStr, "unknown database") {
		return true
	}
	// Dolt internal race: after CREATE DATABASE, information_schema queries
	// on the new database may fail with "no root value found in session" if
	// the server hasn't finished initializing the database's root value.
	// This is transient and resolves on retry.
	if strings.Contains(errStr, "no root value found") {
		return true
	}
	return false
}

// isLockError returns true if the error indicates a Dolt lock contention problem.
// These can occur when the Dolt server's storage layer is locked by another
// process or a stale LOCK file was left behind by a crashed server.
func isLockError(err error) bool {
	if err == nil {
		return false
	}
	errStr := strings.ToLower(err.Error())
	return strings.Contains(errStr, "database is locked") ||
		strings.Contains(errStr, "lock file") ||
		strings.Contains(errStr, "noms lock") ||
		strings.Contains(errStr, "locked by another dolt process")
}

// wrapLockError wraps lock-related errors with actionable guidance.
// Non-lock errors and nil are returned unchanged.
func wrapLockError(err error) error {
	if !isLockError(err) {
		return err
	}
	hint := lockProcessHint()
	return fmt.Errorf("%w\n\nThe Dolt database is locked.%s\n"+
		"Try: bd doctor --fix (clears stale locks), or kill the holding process.", err, hint)
}

// lockProcessHint tries to identify the process holding the database lock.
// Returns a hint string like " Process 12345 (bd) may be holding the lock."
// Returns empty string if identification fails or on unsupported platforms.
func lockProcessHint() string {
	// Look for other bd/dolt processes that might hold the lock
	entries, err := os.ReadDir("/proc")
	if err != nil {
		// /proc not available (macOS, Windows, FreeBSD) — skip PID detection
		return ""
	}

	myPID := os.Getpid()
	var holders []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid == myPID {
			continue
		}
		cmdline, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "cmdline"))
		if err != nil {
			continue
		}
		cmd := string(cmdline)
		if strings.Contains(cmd, "bd") || strings.Contains(cmd, "dolt") {
			holders = append(holders, fmt.Sprintf("%d", pid))
		}
	}

	if len(holders) == 0 {
		return ""
	}
	if len(holders) == 1 {
		return fmt.Sprintf(" Process %s (bd/dolt) may be holding the lock.", holders[0])
	}
	return fmt.Sprintf(" Processes %s (bd/dolt) may be holding the lock.", strings.Join(holders, ", "))
}

// withRetry executes an operation with retry for transient errors.
// If a circuit breaker is configured, it checks the breaker before each attempt
// and records connection failures/successes to coordinate fail-fast across processes.
func (s *DoltStore) withRetry(ctx context.Context, op func() error) error {
	// Circuit breaker: fail-fast if the server is known to be down.
	if s.breaker != nil && !s.breaker.Allow() {
		doltMetrics.circuitRejected.Add(ctx, 1)
		return ErrCircuitOpen
	}

	attempts := 0
	bo := newServerRetryBackoff()
	err := backoff.Retry(func() error {
		attempts++
		err := op()
		if err != nil && isRetryableError(err) {
			// Record connection-level failures to the circuit breaker
			if s.breaker != nil && isConnectionError(err) {
				s.breaker.RecordFailure()
				// Check if the breaker just tripped — if so, stop retrying
				if s.breaker.State() == circuitOpen {
					doltMetrics.circuitTrips.Add(ctx, 1)
					return backoff.Permanent(fmt.Errorf("%w (circuit breaker tripped)", err))
				}
			}
			return err // Retryable - backoff will retry
		}
		if err != nil {
			return backoff.Permanent(err) // Non-retryable - stop immediately
		}
		// Success — reset the circuit breaker
		if s.breaker != nil {
			s.breaker.RecordSuccess()
		}
		return nil
	}, backoff.WithContext(bo, ctx))
	if attempts > 1 {
		doltMetrics.retryCount.Add(ctx, int64(attempts-1))
	}
	return err
}
