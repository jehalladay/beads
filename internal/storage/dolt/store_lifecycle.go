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
	"os"
	"path/filepath"

	"github.com/steveyegge/beads/internal/doltserver"
	"github.com/steveyegge/beads/internal/storage/doltutil"
	"github.com/steveyegge/beads/internal/storage/versioncontrolops"
)

// DB returns the underlying sql.DB connection for direct queries.
// Use sparingly — prefer the store's typed methods for normal operations.
func (s *DoltStore) DB() *sql.DB {
	return s.db
}

// RemoteName returns the configured default sync remote name ("origin" unless
// overridden), the remote Push/Pull target when no explicit remote is given.
func (s *DoltStore) RemoteName() string {
	return s.remote
}

// IsClosed returns true if the store has been closed.
func (s *DoltStore) IsClosed() bool {
	return s.closed.Load()
}

// Close closes the database connection and removes any 0-byte noms LOCK files
// left behind by the embedded Dolt engine.
func (s *DoltStore) Close() error {
	s.closed.Store(true)
	s.mu.Lock()
	defer s.mu.Unlock()
	var err error
	if s.db != nil {
		if cerr := doltutil.CloseWithTimeout("db", s.db.Close); cerr != nil {
			// Timeout is non-fatal for cleanup - just log it
			if !errors.Is(cerr, context.Canceled) {
				err = errors.Join(err, cerr)
			}
		}
	}
	s.db = nil

	// Stop auto-started server when the last store referencing it closes.
	if s.autoStartedServerDir != "" {
		if stopErr := autoStartRelease(s.autoStartedServerDir); stopErr != nil {
			// Best-effort: don't mask other errors
			fmt.Fprintf(os.Stderr, "Warning: failed to stop auto-started dolt server: %v\n", stopErr)
		}
		s.autoStartedServerDir = ""
	}

	// WARNING: DO NOT remove, delete, or modify files inside Dolt's .dolt/
	// directory — including noms/LOCK files. These are Dolt-internal files.
	// Removing them WILL cause unrecoverable data corruption and data loss.
	// Dolt manages these files itself; external interference is never safe.

	return err
}

// Path returns the database directory path
func (s *DoltStore) Path() string {
	return s.dbPath
}

// IsReadOnly reports whether the store was opened in read-only mode. It is a
// test-facing accessor: production read-only enforcement comes from the
// read-only open mode itself (the readOnly field guards every write path), not
// from callers consulting this method. Tests such as
// TestDepRoutedTargetOpensReadOnly use it to assert that routed
// dependency/link target resolution opens a by-ID target read-only, so
// resolving it never opens a foreign project writable or runs open-time
// migrations into its history (bd-6dnrw.32, GH#3231).
func (s *DoltStore) IsReadOnly() bool {
	return s.readOnly
}

// CLIDir returns the directory for dolt CLI operations (push/pull/remote/fetch).
// The actual database lives in a subdirectory of Path() named after the database.
// Use this instead of Path() when running dolt CLI commands that target the
// actual database (e.g., remote add/remove, push, pull).
func (s *DoltStore) CLIDir() string {
	if s.serverMode && doltserver.IsSharedServerMode() && s.beadsDir != "" {
		return filepath.Join(doltserver.ResolveDoltDir(s.beadsDir), s.database)
	}
	if s.dbPath == "" {
		return ""
	}
	return filepath.Join(s.dbPath, s.database)
}

// DoltGC runs Dolt garbage collection to reclaim disk space.
// Pins a single connection to avoid session state loss on pooled *sql.DB.
func (s *DoltStore) DoltGC(ctx context.Context) error {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire connection for gc: %w", err)
	}
	defer conn.Close()
	return versioncontrolops.DoltGC(ctx, conn)
}

// Flatten squashes all Dolt commit history into a single commit.
// Pins a single connection because the stored procedures (DOLT_CHECKOUT,
// DOLT_RESET, etc.) rely on session-scoped state that would be lost if
// steps execute on different pooled connections.
func (s *DoltStore) Flatten(ctx context.Context) error {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire connection for flatten: %w", err)
	}
	defer conn.Close()
	return versioncontrolops.Flatten(ctx, conn)
}

// Compact squashes old Dolt commits while preserving recent ones.
// Pins a single connection for session-scoped stored procedures.
func (s *DoltStore) Compact(ctx context.Context, initialHash, boundaryHash string, oldCommits int, recentHashes []string) error {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire connection for compact: %w", err)
	}
	defer conn.Close()
	return versioncontrolops.Compact(ctx, conn, initialHash, boundaryHash, oldCommits, recentHashes)
}

// UnderlyingDB returns the underlying *sql.DB connection
func (s *DoltStore) UnderlyingDB() *sql.DB {
	return s.db
}

// =============================================================================
// Version Control Operations (Dolt-specific extensions)
// =============================================================================
