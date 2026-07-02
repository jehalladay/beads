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
	"fmt"
	"time"

	"github.com/cenkalti/backoff/v4"
	mysql "github.com/go-sql-driver/mysql"

	"github.com/steveyegge/beads/internal/storage/schema"
)

// initSchemaOnDB applies pending schema migrations. schema.MigrateUp tracks
// applied versions in schema_migrations and backfills legacy config-driven
// tables. Returns the number of migrations applied.
func initSchemaOnDB(ctx context.Context, db *sql.DB) (int, error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return 0, fmt.Errorf("schema: pin connection: %w", err)
	}
	defer conn.Close()

	var dbName string
	if err := conn.QueryRowContext(ctx, "SELECT DATABASE()").Scan(&dbName); err != nil {
		return 0, fmt.Errorf("schema: read database name: %w", err)
	}

	applied, err := schema.MigrateUpWithLock(ctx, conn, dbName)
	if err != nil {
		return applied, fmt.Errorf("schema migration: %w", err)
	}
	return applied, nil
}

func initSchemaOnDBWithRetry(ctx context.Context, db *sql.DB) (int, error) {
	return initSchemaOnDBWithRetryAndGate(ctx, db, nil)
}

// initSchemaOnDBWithRetryAndGate is initSchemaOnDBWithRetry with an optional
// pre-migration gate run INSIDE the retry loop. The gate's own reads
// (schema_migrations, dolt_remotes) can hit the same transient Dolt
// startup/catalog races the migration retry absorbs, so gate probe errors are
// retried with them instead of failing the open fast (bd-6dnrw.30); a
// *schema.RemoteMigrateGateError refusal stays permanent.
func initSchemaOnDBWithRetryAndGate(ctx context.Context, db *sql.DB, gate func(context.Context, *sql.DB) error) (int, error) {
	// Schema initialization for server mode is idempotent. Retry transient
	// Dolt startup/catalog races and contended migration-lock attempts so
	// concurrent bd processes converge instead of failing one unlucky waiter.
	schemaBO := backoff.NewExponentialBackOff()
	schemaBO.InitialInterval = 100 * time.Millisecond
	// Must exceed schema.MigrateUpWithLock's 5s GET_LOCK wait so a contended
	// schema migration can time out once and still retry.
	schemaBO.MaxElapsedTime = serverRetryMaxElapsed
	var applied int
	err := backoff.Retry(func() error {
		if gate != nil {
			if gateErr := gate(ctx, db); gateErr != nil {
				if !schema.IsRemoteMigrateGateError(gateErr) && isRetryableError(gateErr) {
					return gateErr
				}
				return backoff.Permanent(gateErr)
			}
		}
		var schemaErr error
		applied, schemaErr = initSchemaOnDB(ctx, db)
		if schemaErr != nil && isRetryableError(schemaErr) {
			return schemaErr
		}
		if schemaErr != nil {
			return backoff.Permanent(schemaErr)
		}
		return nil
	}, backoff.WithContext(schemaBO, ctx))
	return applied, err
}

func (s *DoltStore) initSchema(ctx context.Context) error {
	// Schema migrations can run arbitrarily long (e.g. full-table recomputes
	// such as the is_blocked backfill in migration 0047). The main connection
	// pool sets a 10s ReadTimeout (see buildServerDSN); a slow migration over
	// that pool aborts mid-flight with "i/o timeout" and leaves tables dirty,
	// which then blocks every subsequent migration attempt. Run the migration
	// pass over a dedicated connection with no read/write timeout. Cancellation
	// is governed by the caller's context, not a fixed deadline.
	migDB, err := s.openMigrationDB()
	if err != nil {
		return err
	}
	defer migDB.Close()
	// #4259: refuse to silently apply pending migrations to a remote-backed,
	// already-initialized database — that is how two clones fork the schema.
	// The gate runs inside the retry loop, before each migration attempt: its
	// reads can hit transient startup/catalog races (retryable) while a gate
	// refusal is permanent and never retried into a migration.
	// Use the on-disk fallback: a freshly (auto-)started server can report an
	// empty dolt_remotes table even though remotes are persisted in .dolt/config
	// (GH#2315), so an SQL-only check would miss the remote on the first write
	// open after an upgrade.
	gate := func(ctx context.Context, db *sql.DB) error {
		return schema.CheckRemoteMigrateGateWithRemoteCheck(ctx, db, s.hasPersistedCLIRemote)
	}
	_, err = initSchemaOnDBWithRetryAndGate(ctx, migDB, gate)
	return err
}

// ApplySchemaMigrations runs idempotent schema migrations under the
// per-database advisory lock, with retry for transient lock contention.
// Implements storage.SchemaMigrator.
func (s *DoltStore) ApplySchemaMigrations(ctx context.Context) (int, error) {
	migDB, err := s.openMigrationDB()
	if err != nil {
		return 0, err
	}
	defer migDB.Close()
	return initSchemaOnDBWithRetry(ctx, migDB)
}

// openMigrationDB opens a one-off connection pool for schema migrations with no
// read/write timeout. Migrations may run far longer than the default 10s pool
// timeout, and timing out part-way leaves the database in a dirty, half-migrated
// state. The single connection is closed by the caller once migration completes.
func (s *DoltStore) openMigrationDB() (*sql.DB, error) {
	cfg, err := mysql.ParseDSN(s.connStr)
	if err != nil {
		return nil, fmt.Errorf("failed to parse DSN for migration connection: %w", err)
	}
	cfg.ReadTimeout = 0
	cfg.WriteTimeout = 0
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		return nil, fmt.Errorf("failed to open migration connection: %w", err)
	}
	db.SetMaxOpenConns(1)
	return db, nil
}
