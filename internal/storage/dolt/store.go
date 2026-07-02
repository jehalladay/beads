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
	"hash/fnv"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"github.com/steveyegge/beads/internal/configfile"
	"github.com/steveyegge/beads/internal/doltserver"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
)

// DefaultSQLPort is the default port for dolt sql-server.
const DefaultSQLPort = 3307

// Compile-time interface checks.
var _ storage.DoltStorage = (*DoltStore)(nil)

var _ storage.RawDBAccessor = (*DoltStore)(nil)

var _ storage.StoreLocator = (*DoltStore)(nil)

var _ storage.LifecycleManager = (*DoltStore)(nil)

var _ storage.PendingCommitter = (*DoltStore)(nil)

var _ storage.GarbageCollector = (*DoltStore)(nil)

var _ storage.Flattener = (*DoltStore)(nil)

var _ storage.Compactor = (*DoltStore)(nil)

var _ storage.SchemaMigrator = (*DoltStore)(nil)

// DoltStore implements the Storage interface using Dolt
type DoltStore struct {
	db            *sql.DB
	dbPath        string       // Path to Dolt data directory (server root, e.g. .beads/dolt/)
	beadsDir      string       // Path to .beads directory (parent of dbPath)
	database      string       // Database name (subdirectory under dbPath)
	closed        atomic.Bool  // Tracks whether Close() has been called
	connStr       string       // Connection string for reconnection
	mu            sync.RWMutex // Protects concurrent access
	readOnly      bool         // True if opened in read-only mode
	credentialKey []byte       // Random encryption key for federation credentials

	customStatusDetailedCache []types.CustomStatus
	customStatusCache         []string
	customStatusCached        bool
	customTypeCache           []string
	customTypeCached          bool
	infraTypeCache            map[string]bool
	infraTypeCached           bool
	cacheMu                   sync.Mutex

	// OTel span attribute cache (avoids per-call allocation)
	spanAttrsOnce  sync.Once
	spanAttrsCache []attribute.KeyValue

	// Circuit breaker for Dolt server connections
	breaker *circuitBreaker

	// Version control config
	committerName  string
	committerEmail string
	remote         string // Default remote for push/pull
	branch         string // Current branch
	remoteUser     string // Remote auth user for Hosted Dolt push/pull (optional)
	remotePassword string // Remote auth password for Hosted Dolt push/pull (optional)
	serverMode     bool   // true when connected to external dolt sql-server (not embedded)

	// autoStartedServerDir is set when this store triggered a dolt sql-server
	// auto-start. Close() uses it to stop the server when the last store
	// referencing it is closed (tracked via autoStartRefs).
	autoStartedServerDir string
}

// Config holds Dolt database configuration
type Config struct {
	Path           string // Path to Dolt database directory
	BeadsDir       string // Path to .beads directory (for server auto-start when Path is custom)
	CommitterName  string // Git-style committer name
	CommitterEmail string // Git-style committer email
	Remote         string // Default remote name (e.g., "origin")
	Database       string // Database name within Dolt (default: "beads")
	ReadOnly       bool   // Open in read-only mode (skip schema init)

	// Server connection options
	ServerSocket   string // Unix domain socket path (overrides Host/Port when set)
	ServerHost     string // Server host (default: 127.0.0.1)
	ServerPort     int    // Server port (default: 3307)
	ServerUser     string // MySQL user (default: root)
	ServerPassword string // MySQL password (default: empty, can be set via BEADS_DOLT_PASSWORD)
	ServerTLS      bool   // Enable TLS for server connections (required for Hosted Dolt)

	// Remote auth for Hosted Dolt push/pull (optional)
	// When set, Push/Pull use the --user flag and set DOLT_REMOTE_PASSWORD env var.
	RemoteUser     string // Hosted Dolt remote user (set via DOLT_REMOTE_USER env var)
	RemotePassword string // Hosted Dolt remote password (set via DOLT_REMOTE_PASSWORD env var)

	// SyncRemote holds the effective sync remote URL (from sync.remote
	// or deprecated sync.git-remote). Used for context-aware error hints.
	SyncRemote string

	// CreateIfMissing allows CREATE DATABASE when the target database does not
	// exist on the server. Only explicit initialization, migration, or new-board
	// creation paths should set this to true. Normal open paths leave it false,
	// which causes an error if the database is missing — preventing silent
	// creation of shadow databases on the wrong server.
	CreateIfMissing bool

	// ServerMode indicates this config targets an external dolt sql-server
	// rather than the embedded Dolt engine. Set by the store factory based
	// on metadata.json dolt_mode or BEADS_DOLT_SERVER_MODE env var.
	ServerMode bool

	// ProxiedServer indicates this config targets a per-workspace proxied
	// dolt sql-server (a parent proxy + a child dolt sql-server, both rooted
	// at <BeadsDir>/proxieddb). Mutually exclusive with ServerMode: the
	// proxied path owns its own connection details and does not consult
	// ServerHost/Port/Socket/User. Set by the store factory based on
	// metadata.json dolt_mode=proxied-server.
	ProxiedServer bool

	// AutoStart enables transparent server auto-start when connection fails.
	// When true and the host is localhost, bd will start a dolt sql-server
	// automatically if one isn't running. Disabled under orchestrator (GT_ROOT set).
	AutoStart bool

	// DisableAutoStart suppresses implicit server startup even when standalone
	// defaults would enable it. Diagnostic paths use this to stay read-only.
	DisableAutoStart bool

	// MaxOpenConns overrides the connection pool size (0 = default 10).
	// Set to 1 for branch isolation in tests (DOLT_CHECKOUT is session-level).
	MaxOpenConns int

	// MaxIdleConns overrides the maximum number of idle pooled connections
	// (0 = default min(5, MaxOpenConns)). Higher values keep more connections
	// warm between queries, reducing NewConnection/ConnectionClosed churn.
	MaxIdleConns int

	// ConnMaxLifetime overrides how long a pooled connection may be reused
	// before the pool retires it (0 = default 1 hour). Long-lived daemons
	// should not use a short lifetime — every retire+reopen shows up as a
	// NewConnection event in dolt-server.log and churns the pool for no
	// benefit when the server is local and stable.
	ConnMaxLifetime time.Duration

	// ConnMaxIdleTime overrides how long a connection may sit idle in the pool
	// before the pool retires it (0 = default 20s). This must stay below the
	// dolt sql-server wait_timeout (currently 30s) so the pool retires an idle
	// connection before the server reaps it server-side; otherwise the next
	// query handed a server-reaped connection fails with "invalid connection".
	ConnMaxIdleTime time.Duration
}

// Defaults for the *sql.DB connection pool. Exported for tests/callers that
// want to reason about the out-of-the-box pool limits without having to read
// openServerConnection.
const (
	defaultMaxOpenConns    = 10
	defaultMaxIdleConns    = 5
	defaultConnMaxLifetime = time.Hour
	// defaultConnMaxIdleTime keeps idle pooled connections shorter-lived than the
	// dolt sql-server wait_timeout (30s) so the pool retires an idle connection
	// before the server reaps it; this prevents the next read from picking up a
	// server-closed connection and failing with "invalid connection".
	defaultConnMaxIdleTime = 20 * time.Second
)

// applyConfigDefaults fills in default values for unset Config fields.
func applyConfigDefaults(cfg *Config) {
	if cfg.Database == "" {
		// Check env var first — this is the highest-priority override and
		// must be consulted even when no config file was loaded.
		if d := os.Getenv("BEADS_DOLT_SERVER_DATABASE"); d != "" {
			cfg.Database = d
		} else if os.Getenv("BEADS_TEST_MODE") == "1" && cfg.Path != "" {
			// Test mode: derive unique database name from path for isolation.
			// Each test creates a unique temp directory, so hashing the path
			// gives each test its own database on the shared test server.
			h := fnv.New64a()
			_, _ = h.Write([]byte(cfg.Path)) // hash.Hash.Write never returns an error
			cfg.Database = fmt.Sprintf("testdb_%x", h.Sum64())
		} else {
			fmt.Fprintf(os.Stderr, "warning: no database name configured; falling back to default %q\n", configfile.DefaultDoltDatabase)
			cfg.Database = configfile.DefaultDoltDatabase
		}
	}
	if cfg.CommitterName == "" {
		cfg.CommitterName = os.Getenv("GIT_AUTHOR_NAME")
		if cfg.CommitterName == "" {
			cfg.CommitterName = "beads"
		}
	}
	if cfg.CommitterEmail == "" {
		cfg.CommitterEmail = os.Getenv("GIT_AUTHOR_EMAIL")
		if cfg.CommitterEmail == "" {
			cfg.CommitterEmail = "beads@local"
		}
	}
	if cfg.Remote == "" {
		cfg.Remote = "origin"
	}

	// Server connection defaults (applied in server mode; embedded mode bypasses TCP)
	if cfg.ServerSocket == "" {
		cfg.ServerSocket = os.Getenv("BEADS_DOLT_SERVER_SOCKET")
	}
	if cfg.ServerHost == "" {
		// Host resolution: BEADS_DOLT_SERVER_HOST env > default 127.0.0.1.
		if h := os.Getenv("BEADS_DOLT_SERVER_HOST"); h != "" {
			cfg.ServerHost = h
		} else {
			cfg.ServerHost = "127.0.0.1"
		}
	}
	// Port resolution: BEADS_DOLT_SERVER_PORT env (or legacy BEADS_DOLT_PORT) >
	// BEADS_TEST_MODE guard > metadata config > default.
	// CRITICAL: BEADS_TEST_MODE=1 forces port 1 (immediate fail) if the resolved port
	// is the production port (DefaultSQLPort). This prevents test databases from leaking
	// onto production even when the port env var is set to 3307 by the orchestrator's beads module.
	// Only an explicit non-production port (e.g., 43211 for a test server)
	// overrides test mode — that's a deliberate test server assignment.
	envPort := os.Getenv("BEADS_DOLT_SERVER_PORT")
	if envPort == "" {
		envPort = os.Getenv("BEADS_DOLT_PORT") // legacy fallback
	}
	if envPort != "" {
		if p, err := strconv.Atoi(envPort); err == nil && p > 0 {
			cfg.ServerPort = p
		}
	}
	// If env var didn't provide a port, consult the full resolution chain:
	// port file > config.yaml > metadata.json (GH#2590).
	// Resolve from the owning .beads dir when available; cfg.Path is the Dolt
	// data path, not the config directory, and using it directly can miss the
	// repo-local port file or metadata.
	if cfg.ServerPort == 0 {
		resolveDir := cfg.BeadsDir
		if resolveDir == "" && cfg.Path != "" {
			resolveDir = filepath.Dir(cfg.Path)
		}
		if resolveDir != "" {
			if resolved := doltserver.DefaultConfig(resolveDir); resolved.Port > 0 {
				cfg.ServerPort = resolved.Port
			}
		}
	}
	// Port 0 means "not yet resolved" — auto-start (EnsureRunning) will
	// allocate an ephemeral port. Don't default to 3307 as that caused
	// cross-project data leakage (GH#2098, GH#2372).
	//
	// Test mode guard: force port 1 (immediate fail) if we'd hit production
	// or have no port, to prevent test databases leaking onto production.
	if os.Getenv("BEADS_TEST_MODE") == "1" {
		if cfg.ServerPort == 0 || cfg.ServerPort == DefaultSQLPort {
			cfg.ServerPort = 1
		}
	}
	if cfg.ServerUser == "" {
		cfg.ServerUser = "root"
	}
	// Check environment variable for password (more secure than command-line)
	if cfg.ServerPassword == "" {
		cfg.ServerPassword = os.Getenv("BEADS_DOLT_PASSWORD")
	}

	// Remote credentials for Hosted Dolt push/pull (env vars take precedence)
	if cfg.RemoteUser == "" {
		cfg.RemoteUser = os.Getenv("DOLT_REMOTE_USER")
	}
	if cfg.RemotePassword == "" {
		cfg.RemotePassword = os.Getenv("DOLT_REMOTE_PASSWORD")
	}
}

// New creates a new Dolt storage backend.
// Connects to a running dolt sql-server via MySQL protocol (pure Go).
func New(ctx context.Context, cfg *Config) (*DoltStore, error) {
	if cfg.Path == "" {
		return nil, fmt.Errorf("database path is required")
	}

	applyConfigDefaults(cfg)

	// Hard guard: tests must NEVER connect to the production Dolt server.
	// If BEADS_TEST_MODE=1 and we're about to hit the default prod port,
	// something upstream forgot to set BEADS_DOLT_SERVER_PORT. Panic immediately
	// so the test fails loudly instead of silently polluting prod.
	if os.Getenv("BEADS_TEST_MODE") == "1" && cfg.ServerPort == DefaultSQLPort {
		panic(fmt.Sprintf(
			"BEADS_TEST_MODE=1 but connecting to prod port %d — set BEADS_DOLT_SERVER_PORT or use test helpers (database=%q, path=%q)",
			DefaultSQLPort, cfg.Database, cfg.Path,
		))
	}

	return newServerMode(ctx, cfg)
}

// configCommitMode controls how commitWorkingSet treats the config table, which
// holds both internal keys (issue_prefix) and synced user data (kv.* keys,
// including kv.memory.* persistent memories).
type configCommitMode int

const (
	// configExclude skips config entirely (GH#2455): a plain Commit must not
	// sweep a concurrent writer's half-applied issue_prefix change into an
	// unrelated commit.
	configExclude configCommitMode = iota
	// configIncludeUserKVOnly stages config for the pre-pull auto-commit, but
	// only when every dirty config row is this clone's own user KV data (the
	// kv.* namespace, which includes kv.memory.* memories). Any other dirty
	// config key — an internal key such as issue_prefix above all — aborts the
	// commit with operator guidance so the pull never auto-commits unsafe
	// config (GH#2455 + GH#2474).
	configIncludeUserKVOnly
	// configIncludeAll stages every dirty config row. Used only to conclude a
	// merge whose conflicts the operator resolved explicitly (bd federation
	// sync --strategy): that resolution is intentional, so a resolved
	// issue_prefix (or any config row) must be committed, not dropped.
	configIncludeAll
)
