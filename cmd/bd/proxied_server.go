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

const (
	proxiedServerRootName   = "proxieddb"
	proxiedServerConfigName = "server_config.yaml"
	proxiedServerLogName    = "server.log"
)

func proxiedServerRoot(beadsDir string) string {
	return filepath.Join(beadsDir, proxiedServerRootName)
}

func proxiedServerConfigPath(beadsDir string) string {
	return filepath.Join(proxiedServerRoot(beadsDir), proxiedServerConfigName)
}

func proxiedServerLogPath(beadsDir string) string {
	return filepath.Join(proxiedServerRoot(beadsDir), proxiedServerLogName)
}

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

const proxiedServerListenerHost = "127.0.0.1"

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
