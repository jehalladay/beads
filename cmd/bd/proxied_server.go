package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/dolthub/dolt/go/libraries/doltcore/servercfg"
	"gopkg.in/yaml.v3"

	"github.com/steveyegge/beads/internal/config"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/storage/db/proxy"
	"github.com/steveyegge/beads/internal/storage/dolt"
	proxieddolt "github.com/steveyegge/beads/internal/storage/doltserver"
)

// Filesystem layout for --proxied-server (per workspace, all under one dir):
//
//	<beadsDir>/proxieddb/                       proxy lockfile, pidfile,
//	                                            child dolt repository
//	<beadsDir>/proxieddb/server_config.yaml     dolt sql-server YAML
//	                                            (host / port / log_level)
//	<beadsDir>/proxieddb/server.log             daemon stdout/stderr
//
// The whole directory is owned by the per-workspace proxied dolt sql-server
// feature and is gitignored (the bound port is host-specific).
const (
	proxiedServerRootName   = "proxieddb"
	proxiedServerConfigName = "server_config.yaml"
	proxiedServerLogName    = "server.log"
)

// proxiedServerRoot returns <beadsDir>/proxieddb — the rootDir for the proxy
// lockfile/pidfile, the child dolt repository, and the server config + log.
func proxiedServerRoot(beadsDir string) string {
	return filepath.Join(beadsDir, proxiedServerRootName)
}

// proxiedServerConfigPath returns <beadsDir>/proxieddb/server_config.yaml —
// the YAML config file consumed by dolt sql-server (parsed via
// servercfg.YamlConfigFromFile in internal/storage/db/server/doltserver.go).
func proxiedServerConfigPath(beadsDir string) string {
	return filepath.Join(proxiedServerRoot(beadsDir), proxiedServerConfigName)
}

// proxiedServerLogPath returns <beadsDir>/proxieddb/server.log — where the
// dolt sql-server daemon writes stdout/stderr.
func proxiedServerLogPath(beadsDir string) string {
	return filepath.Join(proxiedServerRoot(beadsDir), proxiedServerLogName)
}

// ensureProxiedServerConfig returns the path to the proxied dolt sql-server
// YAML config, creating it (and the proxieddb root directory) with a freshly
// picked free port if it does not yet exist.
//
// Idempotent: existing files are returned untouched so the dolt sql-server
// keeps the port it was launched with across bd invocations. Re-picking the
// port on every call would race the already-running daemon (which is bound
// to the existing port) and require a restart on every bd command.
//
// Owned by the store-open path, not by `bd init`: the proxied-server store
// cannot run without this file, so every code path that opens the store is
// responsible for ensuring it. Init is just another caller.
func ensureProxiedServerConfig(beadsDir string) (string, error) {
	root := proxiedServerRoot(beadsDir)
	if err := os.MkdirAll(root, config.BeadsDirPerm); err != nil {
		return "", fmt.Errorf("ensureProxiedServerConfig: mkdir %s: %w", root, err)
	}
	path := proxiedServerConfigPath(beadsDir)

	switch _, err := os.Stat(path); {
	case err == nil:
		return path, nil
	case !os.IsNotExist(err):
		return "", fmt.Errorf("ensureProxiedServerConfig: stat %s: %w", path, err)
	}

	port, err := proxy.PickFreePort()
	if err != nil {
		return "", fmt.Errorf("ensureProxiedServerConfig: pick free port: %w", err)
	}

	body, err := renderProxiedServerConfig(port)
	if err != nil {
		return "", fmt.Errorf("ensureProxiedServerConfig: render YAML: %w", err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		return "", fmt.Errorf("ensureProxiedServerConfig: write %s: %w", path, err)
	}
	return path, nil
}

// renderProxiedServerConfig produces the dolt sql-server YAML body for the
// proxied-server backend. We construct dolt's typed servercfg.YAMLConfig and
// marshal it rather than hand-writing strings: every field carries its yaml
// tag from the upstream package, so the schema stays in lockstep with
// whatever version of dolt this binary links against.
//
// Only fields the proxied-server cares about are populated — listener
// host/port (forced to 127.0.0.1 + the picked port) and log_level. Everything
// else is left zero so the upstream `omitempty` tags suppress it.
func renderProxiedServerConfig(port int) ([]byte, error) {
	host := proxiedServerListenerHost
	logLevel := string(servercfg.LogLevel_Info)
	yc := &servercfg.YAMLConfig{
		LogLevelStr: &logLevel,
		ListenerConfig: servercfg.ListenerYAMLConfig{
			HostStr:    &host,
			PortNumber: &port,
		},
	}
	return yaml.Marshal(yc)
}

// proxiedServerListenerHost is the host the child dolt sql-server binds.
// Always loopback — the proxy multiplexes external clients onto this socket
// and we don't want the child accidentally exposed on a routable interface.
const proxiedServerListenerHost = "127.0.0.1"

// newProxiedServerStore constructs a storage.DoltStorage backed by a
// per-workspace proxied dolt sql-server. It is the cmd/bd-side glue that
// resolves all of the per-workspace dependencies — dolt binary, YAML config
// path, log path, committer identity — and hands them to
// proxieddolt.NewDoltServerStore. Called from newDoltStore (store_factory.go
// and store_factory_nocgo.go) when cfg.ProxiedServer is true.
func newProxiedServerStore(ctx context.Context, cfg *dolt.Config) (storage.DoltStorage, error) {
	if cfg == nil {
		return nil, fmt.Errorf("newProxiedServerStore: cfg is nil")
	}
	if cfg.BeadsDir == "" {
		return nil, fmt.Errorf("newProxiedServerStore: cfg.BeadsDir must be set")
	}
	if cfg.Database == "" {
		return nil, fmt.Errorf("newProxiedServerStore: cfg.Database must be set")
	}

	doltBin, err := exec.LookPath("dolt")
	if err != nil {
		return nil, fmt.Errorf("newProxiedServerStore: dolt is not installed (not found in PATH); install from https://docs.dolthub.com/introduction/installation: %w", err)
	}

	configPath, err := ensureProxiedServerConfig(cfg.BeadsDir)
	if err != nil {
		return nil, err
	}

	name, email := cfg.CommitterName, cfg.CommitterEmail
	if name == "" || email == "" {
		fallbackName, fallbackEmail := proxiedServerCommitter()
		if name == "" {
			name = fallbackName
		}
		if email == "" {
			email = fallbackEmail
		}
	}

	return proxieddolt.NewDoltServerStore(
		ctx,
		proxiedServerRoot(cfg.BeadsDir),
		cfg.BeadsDir,
		cfg.Database,
		name, email,
		proxiedServerLogPath(cfg.BeadsDir),
		configPath,
		proxy.BackendLocalServer,
		false, // autoSyncToOriginRemote — wired in a future iteration
		"root",
		"", // rootPassword: proxy is loopback-only, no auth
		doltBin,
	)
}

// proxiedServerCommitter returns the (name, email) recorded on dolt commits
// made by the proxied-server store. Sourced from `git config user.name/email`
// when present, else falls back to ("beads", "beads@localhost"). Mirrors the
// behavior of the dolt sql-server's own auto-configure path
// (internal/storage/db/server/doltserver.go: doltConfigure).
func proxiedServerCommitter() (string, string) {
	name, email := "beads", "beads@localhost"
	if out, err := exec.Command("git", "config", "user.name").Output(); err == nil {
		if v := strings.TrimSpace(string(out)); v != "" {
			name = v
		}
	}
	if out, err := exec.Command("git", "config", "user.email").Output(); err == nil {
		if v := strings.TrimSpace(string(out)); v != "" {
			email = v
		}
	}
	return name, email
}
