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
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/cenkalti/backoff/v4"
	mysql "github.com/go-sql-driver/mysql"

	"github.com/steveyegge/beads/internal/configfile"
	"github.com/steveyegge/beads/internal/doltserver"
	"github.com/steveyegge/beads/internal/storage/doltutil"
	"github.com/steveyegge/beads/internal/storage/schema"
)

// testDatabasePrefixes are name prefixes that indicate a test database.
// Used by isTestDatabaseName to prevent test databases from being created
// on the production Dolt server (Clown Shows #12-#18).
var testDatabasePrefixes = []string{
	"testdb_",
	"beads_test",
	"beads_pt",
	"beads_vr",
	"doctest_",
	"doctortest_",
}

// isTestDatabaseName returns true if the database name matches known test patterns.
// This is a pattern-based firewall — it does not rely on environment variables.
func isTestDatabaseName(name string) bool {
	for _, prefix := range testDatabasePrefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// autoStartRefs tracks in-process reference counts for auto-started dolt
// sql-server processes, keyed by resolved server directory. When the count
// drops to zero, the server is stopped. This prevents test-started servers
// from leaking (GH#2542) while allowing multiple stores to share one server.
// Normal repo-local auto-starts are intentionally not tracked here: those
// servers should stay up like an explicit `bd dolt start`, rather than being
// torn down at the end of each command.
var autoStartRefs struct {
	mu sync.Mutex
	m  map[string]int
}

func autoStartAcquire(serverDir string) {
	autoStartRefs.mu.Lock()
	defer autoStartRefs.mu.Unlock()
	if autoStartRefs.m == nil {
		autoStartRefs.m = make(map[string]int)
	}
	autoStartRefs.m[serverDir]++
}

// autoStartAcquireExisting increments the refcount for serverDir only when the
// current process is already tracking that auto-started server. This lets later
// stores share the same test-owned server without taking ownership of servers
// started by other processes.
func autoStartAcquireExisting(serverDir string) bool {
	autoStartRefs.mu.Lock()
	defer autoStartRefs.mu.Unlock()
	if autoStartRefs.m == nil || autoStartRefs.m[serverDir] <= 0 {
		return false
	}
	autoStartRefs.m[serverDir]++
	return true
}

// autoStartRelease decrements the refcount for serverDir and stops the server
// when it reaches zero. Returns any error from stopping the server.
// If the server is already stopped (e.g. killed externally, or never started),
// the ErrServerNotRunning sentinel is silently absorbed to avoid false
// "failed to stop" warnings (GH#2670).
func autoStartRelease(serverDir string) error {
	autoStartRefs.mu.Lock()
	defer autoStartRefs.mu.Unlock()
	if autoStartRefs.m == nil {
		return nil
	}
	autoStartRefs.m[serverDir]--
	if autoStartRefs.m[serverDir] <= 0 {
		delete(autoStartRefs.m, serverDir)
		// Stop is idempotent: returns ErrServerNotRunning (possibly joined
		// with cleanup errors) when the server is already gone. Strip the
		// sentinel but propagate any real cleanup failures.
		return doltserver.IgnoreNotRunning(doltserver.Stop(serverDir))
	}
	return nil
}

// shouldStopAutoStartedServerOnClose reports whether an auto-started server
// should be treated as test-owned cleanup state instead of a normal repo-local
// server. In real repos, auto-start should behave like a persistent helper
// server, not a single-command subprocess.
func shouldStopAutoStartedServerOnClose(cfg *Config) bool {
	if os.Getenv("BEADS_TEST_MODE") == "1" {
		return true
	}
	return isTestDatabaseName(cfg.Database)
}

// fsckTimeout is the maximum time to wait for dolt fsck to verify the local
// chunk store before a push. fsck reads local files only; 30 seconds is ample
// for any DB size we currently operate.
const fsckTimeout = 30 * time.Second

// newServerMode creates a DoltStore connected to a running dolt sql-server.
// This path is pure Go and does not require CGO.
func newServerMode(ctx context.Context, cfg *Config) (*DoltStore, error) {
	// Clean stale circuit breaker files before checking — prevents leftover
	// state from previous sessions poisoning fresh inits (GH#2598).
	CleanStaleCircuitBreakerFiles()

	breaker := maybeNewCircuitBreaker(cfg.ServerHost, cfg.ServerPort, cfg.Database)

	// Circuit breaker: fail-fast if the server is known to be down.
	if breaker != nil && !breaker.Allow() {
		doltMetrics.circuitRejected.Add(ctx, 1)
		return nil, ErrCircuitOpen
	}

	// Tracks server dir if we auto-started a server (for cleanup in Close, GH#2542).
	var autoStartedDir string
	trackAutoStartedServer := shouldStopAutoStartedServerOnClose(cfg)
	resolvedBeadsDir := cfg.BeadsDir
	if resolvedBeadsDir == "" {
		resolvedBeadsDir = filepath.Dir(cfg.Path) // fallback: cfg.Path is .beads/dolt → parent is .beads/
	}
	serverDir := doltserver.ResolveServerDir(resolvedBeadsDir)

	// Fail-fast connectivity check before MySQL protocol initialization.
	// This gives an immediate, clear error if the Dolt server isn't running,
	// rather than waiting for MySQL driver timeouts.
	var addr string
	var conn net.Conn
	var dialErr error
	if cfg.ServerSocket != "" {
		addr = cfg.ServerSocket
		conn, dialErr = net.DialTimeout("unix", cfg.ServerSocket, 500*time.Millisecond)
	} else {
		addr = net.JoinHostPort(cfg.ServerHost, fmt.Sprintf("%d", cfg.ServerPort))
		conn, dialErr = net.DialTimeout("tcp", addr, 500*time.Millisecond)
	}
	if dialErr != nil {
		// Auto-start: if enabled and connecting locally via TCP, start a server.
		// Socket mode is excluded — auto-start creates a TCP listener, not a
		// unix socket, so the DSN would still fail. Socket users are expected
		// to manage their own server lifecycle.
		canAutoStart := cfg.AutoStart && cfg.Path != "" &&
			cfg.ServerSocket == "" && isLocalHost(cfg.ServerHost)
		if canAutoStart {
			port, startedByUs, startErr := doltserver.EnsureRunningDetailed(resolvedBeadsDir)
			if startErr != nil {
				return nil, fmt.Errorf("Dolt server unreachable at %s and auto-start failed: %w\n\n"+
					"To start manually: bd dolt start\n"+
					"To disable auto-start: set dolt.auto-start: false in .beads/config.yaml",
					addr, startErr)
			}
			// Only tests should stop auto-started servers on Close(). In normal
			// repo-local server mode, leaving the server up avoids endpoint churn
			// and circuit-breaker trips between commands.
			if startedByUs && trackAutoStartedServer {
				autoStartedDir = serverDir
				autoStartAcquire(autoStartedDir)
			}
			// Update port — EnsureRunning allocates an ephemeral port
			if port != cfg.ServerPort {
				if cfg.ServerPort > 0 {
					fmt.Fprintf(os.Stderr, "Warning: Dolt server endpoint changed: port %d → %d (auto-start)\n", cfg.ServerPort, port)
					fmt.Fprintf(os.Stderr, "  Previous port was unreachable. If other tools expect port %d, they may see stale data.\n", cfg.ServerPort)
					fmt.Fprintf(os.Stderr, "  To pin a port: set dolt.port in .beads/config.yaml\n")
				}
				cfg.ServerPort = port
				addr = net.JoinHostPort(cfg.ServerHost, fmt.Sprintf("%d", cfg.ServerPort))
				breaker = maybeNewCircuitBreaker(cfg.ServerHost, cfg.ServerPort, cfg.Database)
			}
			// Retry connection with longer timeout (server just started)
			conn, dialErr = net.DialTimeout("tcp", addr, 2*time.Second)
			if dialErr != nil {
				// Release auto-start ref on connection failure
				if autoStartedDir != "" {
					_ = autoStartRelease(autoStartedDir)
				}
				if breaker != nil {
					breaker.RecordFailure()
				}
				return nil, fmt.Errorf("Dolt server auto-started but still unreachable at %s: %w\n\n"+
					"Check logs: %s", addr, dialErr, doltserver.LogPath(resolvedBeadsDir))
			}
		} else {
			if breaker != nil {
				breaker.RecordFailure()
			}
			var hint string
			if cfg.ServerSocket != "" {
				hint = fmt.Sprintf("The Dolt server is not listening on socket %s.\n"+
					"Ensure the server is started with --socket:\n"+
					"  dolt sql-server --socket %s\n"+
					"Auto-start is not supported in socket mode.",
					cfg.ServerSocket, cfg.ServerSocket)
			} else if !cfg.AutoStart && doltserver.IsAutoStartDisabled() {
				hint = "Dolt server auto-start is disabled (dolt.auto-start: false).\n" +
					"Start the server manually:\n  bd dolt start"
			} else {
				hint = "The Dolt server may not be running. Try:\n  bd dolt start"
			}
			return nil, fmt.Errorf("Dolt server unreachable at %s: %w\n\n%s",
				addr, dialErr, hint)
		}
	}
	_ = conn.Close()

	// If this process already owns a test-started auto-start server, later
	// stores sharing it must participate in the refcount so one Close() does
	// not stop the server out from under another open store.
	if autoStartedDir == "" && trackAutoStartedServer && autoStartAcquireExisting(serverDir) {
		autoStartedDir = serverDir
	}

	// TCP dial succeeded — record success to reset the breaker
	if breaker != nil {
		breaker.RecordSuccess()
	}

	// Server mode: connect via MySQL protocol to dolt sql-server
	db, connStr, err := openServerConnection(ctx, cfg)
	if err != nil {
		return nil, err
	}

	// Test connection
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("failed to ping Dolt database: %w", err)
	}

	beadsDir := cfg.BeadsDir
	if beadsDir == "" && cfg.Path != "" {
		beadsDir = filepath.Dir(cfg.Path) // cfg.Path is .beads/dolt → parent is .beads/
	}

	store := &DoltStore{
		db:                   db,
		dbPath:               cfg.Path,
		beadsDir:             beadsDir,
		database:             cfg.Database,
		connStr:              connStr,
		breaker:              breaker,
		committerName:        cfg.CommitterName,
		committerEmail:       cfg.CommitterEmail,
		remote:               cfg.Remote,
		branch:               "main",
		remoteUser:           cfg.RemoteUser,
		remotePassword:       cfg.RemotePassword,
		serverMode:           true,
		readOnly:             cfg.ReadOnly,
		autoStartedServerDir: autoStartedDir,
	}

	if cfg.ReadOnly {
		if err := schema.CheckForwardDrift(ctx, db); err != nil {
			_ = db.Close()
			return nil, err
		}
	} else {
		if err := store.initSchema(ctx); err != nil {
			return nil, fmt.Errorf("failed to initialize schema: %w", err)
		}
	}

	if !cfg.CreateIfMissing {
		var verifyErr error
		if cfg.Database == doltserver.GlobalDatabaseName {
			verifyErr = store.verifyGlobalProjectIdentity(ctx, cfg.BeadsDir)
		} else {
			verifyErr = store.verifyProjectIdentity(ctx, cfg.BeadsDir)
		}
		if verifyErr != nil {
			_ = db.Close()
			return nil, verifyErr
		}
	}

	if isLocalHost(cfg.ServerHost) && shouldPersistResolvedPortFile() {
		beadsDir := cfg.BeadsDir
		if beadsDir == "" && cfg.Path != "" {
			beadsDir = filepath.Dir(cfg.Path)
		}
		_ = doltserver.EnsurePortFile(beadsDir, cfg.ServerPort)
	}

	// All writers operate on main — transaction isolation via RunInTransaction
	// replaces the former branch-per-worker approach (BD_BRANCH).
	store.branch = "main"

	// Register observable pool gauges for diagnosing shared-server degradation (GH#3140).
	// These report sql.DB.Stats() on each OTel scrape — no-op when telemetry is off.
	store.registerPoolGauges()

	return store, nil
}

func shouldPersistResolvedPortFile() bool {
	return os.Getenv("BEADS_DOLT_SERVER_PORT") == "" && os.Getenv("BEADS_DOLT_PORT") == ""
}

// verifyProjectIdentity checks that the database belongs to the expected project.
// If both the local metadata.json and the database have a project_id, they must match.
// Returns nil if verification passes or is not applicable (missing IDs = old setup).
func (s *DoltStore) verifyProjectIdentity(ctx context.Context, beadsDir string) error {
	if beadsDir == "" {
		return nil // can't verify without knowing beadsDir
	}

	// Load local project ID from metadata.json
	metaCfg, err := configfile.Load(beadsDir)
	if err != nil || metaCfg == nil {
		return nil // no local config — skip verification
	}
	localID := metaCfg.ProjectID
	if localID == "" {
		return nil // old-style metadata.json without project_id — skip
	}

	// Read project ID from database metadata table
	dbID, err := s.GetMetadata(ctx, "_project_id")
	if err != nil || dbID == "" {
		return nil // old database without project_id — skip
	}

	if localID != dbID {
		return fmt.Errorf(
			"PROJECT IDENTITY MISMATCH — refusing to connect\n\n"+
				"  Local project ID (metadata.json):  %s\n"+
				"  Database project ID:               %s\n\n"+
				"This means the Dolt server is serving a DIFFERENT project's database.\n"+
				"This can happen when:\n"+
				"  - Another project's server is running on the same port\n"+
				"  - The server restarted with a different data directory\n\n"+
				"To diagnose: bd dolt status\n"+
				"Do NOT run 'bd init' — your data likely exists, just on a different server.",
			localID, dbID)
	}
	return nil
}

func (s *DoltStore) verifyGlobalProjectIdentity(ctx context.Context, beadsDir string) error {
	if beadsDir == "" {
		return nil
	}

	metaCfg, err := configfile.Load(beadsDir)
	if err != nil || metaCfg == nil {
		return nil
	}
	expectedID := metaCfg.GlobalProjectID
	if expectedID == "" {
		return nil
	}

	dbID, err := s.GetMetadata(ctx, "_project_id")
	if err != nil || dbID == "" {
		return nil
	}

	if expectedID != dbID {
		return fmt.Errorf(
			"GLOBAL PROJECT IDENTITY MISMATCH — refusing to connect\n\n"+
				"  Expected global project ID (metadata.json): %s\n"+
				"  Database project ID:                        %s\n\n"+
				expectedID, dbID)
	}
	return nil
}

// isLocalHost returns true if the host refers to the local machine.
func isLocalHost(host string) bool {
	switch host {
	case "", "127.0.0.1", "localhost", "::1", "[::1]":
		return true
	}
	return false
}

// buildServerDSN constructs a MySQL DSN for connecting to a Dolt server.
// If database is empty, connects without selecting a database (for init operations).
// Adds ReadTimeout/WriteTimeout for long-lived connection pools.
func buildServerDSN(cfg *Config, database string) string {
	base := doltutil.ServerDSN{
		Socket:   cfg.ServerSocket,
		Host:     cfg.ServerHost,
		Port:     cfg.ServerPort,
		User:     cfg.ServerUser,
		Password: cfg.ServerPassword,
		Database: database,
		TLS:      cfg.ServerTLS,
	}
	// Parse the base DSN and add pool-specific timeouts.
	parsed, err := mysql.ParseDSN(base.String())
	if err != nil {
		return base.String()
	}
	parsed.ReadTimeout = 10 * time.Second
	parsed.WriteTimeout = 10 * time.Second
	return parsed.FormatDSN()
}

// applyPoolLimits configures the pool on db using the sensible-default
// connection pool limits, overridden by any non-zero Config fields.
//
// These limits are deliberately oriented at long-lived daemons: a 1h
// connection lifetime lets the same physical MySQL connection be reused
// for thousands of queries, so dolt-server.log no longer shows a
// NewConnection/ConnectionClosed pair every few queries.
func applyPoolLimits(db *sql.DB, cfg *Config) {
	maxOpen := defaultMaxOpenConns
	if cfg.MaxOpenConns > 0 {
		maxOpen = cfg.MaxOpenConns
	}

	maxIdle := defaultMaxIdleConns
	if cfg.MaxIdleConns > 0 {
		maxIdle = cfg.MaxIdleConns
	}
	// MaxIdleConns must never exceed MaxOpenConns or database/sql silently
	// clamps it and we end up with a different pool shape than requested.
	if maxIdle > maxOpen {
		maxIdle = maxOpen
	}

	lifetime := defaultConnMaxLifetime
	if cfg.ConnMaxLifetime > 0 {
		lifetime = cfg.ConnMaxLifetime
	}

	idle := defaultConnMaxIdleTime
	if cfg.ConnMaxIdleTime > 0 {
		idle = cfg.ConnMaxIdleTime
	}

	db.SetMaxOpenConns(maxOpen)
	db.SetMaxIdleConns(maxIdle)
	db.SetConnMaxLifetime(lifetime)
	db.SetConnMaxIdleTime(idle)
}

// openServerConnection opens a connection to a dolt sql-server via MySQL protocol
func openServerConnection(ctx context.Context, cfg *Config) (*sql.DB, string, error) {
	connStr := buildServerDSN(cfg, cfg.Database)

	db, err := sql.Open("mysql", connStr)
	if err != nil {
		return nil, "", fmt.Errorf("failed to open Dolt server connection: %w", err)
	}

	// Configure the pool. *sql.DB is safe for concurrent use and manages its
	// own pool — the same Store reuses these connections across every query
	// for the lifetime of the daemon, rather than opening a fresh one each
	// time (which used to show up as endless NewConnection/ConnectionClosed
	// pairs in dolt-server.log).
	applyPoolLimits(db, cfg)

	// Ensure database exists (may need to create it)
	// First connect without database to create it
	initConnStr := buildServerDSN(cfg, "")
	initDB, err := sql.Open("mysql", initConnStr)
	if err != nil {
		_ = db.Close()
		return nil, "", fmt.Errorf("failed to open init connection: %w", err)
	}
	defer func() { _ = initDB.Close() }()

	// Validate database name to prevent SQL injection via backtick escaping
	if err := ValidateDatabaseName(cfg.Database); err != nil {
		_ = db.Close()
		return nil, "", fmt.Errorf("invalid database name %q: %w", cfg.Database, err)
	}

	// FIREWALL: Never create test databases on the production server.
	// This is the last line of defense against test pollution (Clown Shows #12-#18).
	// Pattern-based, not env-var-based — env vars can be misconfigured or missing.
	if isTestDatabaseName(cfg.Database) && cfg.ServerPort == DefaultSQLPort {
		_ = db.Close()
		return nil, "", fmt.Errorf(
			"REFUSED: will not CREATE DATABASE %q on production port %d — "+
				"this is a test database name on the production server (see DOLT-WAR-ROOM.md)",
			cfg.Database, cfg.ServerPort)
	}

	// Check if the database already exists before deciding whether to create it.
	// This prevents the shadow database bug: without CreateIfMissing, connecting
	// to a server that lacks the expected database is an error (not silent creation).
	//
	// Uses SHOW DATABASES + iterate for exact match instead of SHOW DATABASES LIKE,
	// because LIKE treats _ and % as wildcards and Dolt does not support backslash
	// escaping. Database names like "beads_vulcan" contain underscores which would
	// match unrelated databases with LIKE.
	dbExists, checkErr := databaseExistsOnServer(ctx, initDB, cfg.Database)
	if checkErr != nil {
		_ = db.Close()
		return nil, "", fmt.Errorf("failed to check if database %q exists on server %s:%d: %w",
			cfg.Database, cfg.ServerHost, cfg.ServerPort, checkErr)
	}

	if !dbExists {
		if !cfg.CreateIfMissing {
			_ = db.Close()
			return nil, "", databaseNotFoundError(cfg)
		}

		_, err = initDB.ExecContext(ctx, fmt.Sprintf("CREATE DATABASE IF NOT EXISTS `%s`", cfg.Database)) //nolint:gosec // G201: cfg.Database validated by ValidateDatabaseName above
		if err != nil {
			// Dolt may return error 1007 even with IF NOT EXISTS - ignore if database already exists
			errLower := strings.ToLower(err.Error())
			if !strings.Contains(errLower, "database exists") && !strings.Contains(errLower, "1007") {
				_ = db.Close()
				// Check for connection refused - server likely not running
				if strings.Contains(errLower, "connection refused") || strings.Contains(errLower, "connect: connection refused") {
					return nil, "", fmt.Errorf("failed to connect to Dolt server at %s:%d: %w\n\nThe Dolt server may not be running. Try:\n  bd dolt start    # Start a local server\n  gt dolt start    # If using an orchestrator",
						cfg.ServerHost, cfg.ServerPort, err)
				}
				return nil, "", fmt.Errorf("failed to create database: %w", err)
			}
			// Database already exists - that's fine, continue
		}
	}

	// Wait for the Dolt server's in-memory catalog to register the new database.
	// After CREATE DATABASE, there is a race where the server has created the
	// database on disk but hasn't updated its catalog yet. Pinging db (which
	// has the database in the DSN) will fail with "Unknown database" until the
	// catalog catches up. We retry with exponential backoff. (GH-1851)
	bo := backoff.NewExponentialBackOff()
	bo.InitialInterval = 100 * time.Millisecond
	bo.MaxElapsedTime = 10 * time.Second
	if err := backoff.Retry(func() error {
		pingErr := db.PingContext(ctx)
		if pingErr != nil && isRetryableError(pingErr) {
			return pingErr // retryable — backoff will retry
		}
		if pingErr != nil {
			return backoff.Permanent(pingErr)
		}
		return nil
	}, backoff.WithContext(bo, ctx)); err != nil {
		_ = db.Close()
		return nil, "", fmt.Errorf("database %q not available after CREATE DATABASE: %w", cfg.Database, err)
	}

	return db, connStr, nil
}

// databaseExistsOnServer checks if a database with the exact given name exists
// on the Dolt server. Uses SHOW DATABASES + iterate instead of SHOW DATABASES LIKE
// to avoid LIKE wildcard issues with underscores in database names.
func databaseExistsOnServer(ctx context.Context, db *sql.DB, name string) (bool, error) {
	rows, err := db.QueryContext(ctx, "SHOW DATABASES")
	if err != nil {
		return false, err
	}
	defer rows.Close()

	for rows.Next() {
		var dbName string
		if err := rows.Scan(&dbName); err != nil {
			return false, err
		}
		if dbName == name {
			return true, nil
		}
	}
	return false, rows.Err()
}
