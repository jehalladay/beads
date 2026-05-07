package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dolthub/dolt/go/libraries/doltcore/servercfg"
	"github.com/dolthub/dolt/go/libraries/utils/filesys"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRenderProxiedServerConfig_RoundTrips(t *testing.T) {
	body, err := renderProxiedServerConfig(54321)
	require.NoError(t, err)

	cfg, err := servercfg.NewYamlConfig(body)
	require.NoError(t, err)

	assert.Equal(t, proxiedServerListenerHost, cfg.Host(), "Host mismatch")
	assert.Equal(t, 54321, cfg.Port(), "Port mismatch")
	assert.Equal(t, servercfg.LogLevel_Info, cfg.LogLevel(), "LogLevel mismatch")
}

func TestEnsureProxiedServerConfig_CreatesAndIsIdempotent(t *testing.T) {
	beadsDir := t.TempDir()

	path1, err := ensureProxiedServerConfig(beadsDir)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(beadsDir, "proxieddb", "server_config.yaml"), path1)

	body1, err := os.ReadFile(path1)
	require.NoError(t, err)
	require.NotEmpty(t, body1)
	require.True(t, strings.Contains(string(body1), proxiedServerListenerHost))

	// Second call must NOT rewrite — running daemon is bound to the existing port.
	path2, err := ensureProxiedServerConfig(beadsDir)
	require.NoError(t, err)
	assert.Equal(t, path1, path2)

	body2, err := os.ReadFile(path2)
	require.NoError(t, err)
	assert.Equal(t, body1, body2, "second call must not rewrite the file")

	// Round-trip: dolt's own loader must accept what we wrote.
	loaded, err := servercfg.YamlConfigFromFile(filesys.LocalFS, path2)
	require.NoError(t, err)
	assert.Equal(t, proxiedServerListenerHost, loaded.Host())
	assert.Greater(t, loaded.Port(), 0)
}

func TestProxiedServerPathHelpers(t *testing.T) {
	bd := "/tmp/some/.beads"
	assert.Equal(t, "/tmp/some/.beads/proxieddb", proxiedServerRoot(bd))
	assert.Equal(t, "/tmp/some/.beads/proxieddb/server_config.yaml", proxiedServerConfigPath(bd))
	assert.Equal(t, "/tmp/some/.beads/proxieddb/server.log", proxiedServerLogPath(bd))
}

// TestInitCommandRegistersProxiedServerFlag verifies the --proxied-server flag
// is wired into initCmd. Flag-presence regression test.
func TestInitCommandRegistersProxiedServerFlag(t *testing.T) {
	flag := initCmd.Flags().Lookup("proxied-server")
	require.NotNil(t, flag, "init command does not register --proxied-server")
	assert.Equal(t, "false", flag.DefValue, "--proxied-server should default to false")
}

// TestCheckExistingBeadsDataAt_ProxiedServerNoData asserts that a proxied
// workspace with metadata.json but no actual <beadsDir>/proxieddb/<dbName>/.dolt
// directory is treated as a fresh clone — init is allowed to proceed so the
// caller can bootstrap.
func TestCheckExistingBeadsDataAt_ProxiedServerNoData(t *testing.T) {
	beadsDir := filepath.Join(t.TempDir(), ".beads")
	require.NoError(t, os.MkdirAll(beadsDir, 0o755))

	metadata := map[string]interface{}{
		"database":      "dolt",
		"backend":       "dolt",
		"dolt_mode":     "proxied-server",
		"dolt_database": "myproj",
	}
	data, err := json.Marshal(metadata)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(beadsDir, "metadata.json"), data, 0o644))

	// No <beadsDir>/proxieddb/myproj/.dolt — fresh-clone scenario.
	if err := checkExistingBeadsDataAt(beadsDir, "myproj"); err != nil {
		t.Fatalf("fresh proxied workspace should allow init, got: %v", err)
	}
}

// TestCheckExistingBeadsDataAt_ProxiedServerWithExistingDB asserts that the
// mere existence of <beadsDir>/proxieddb/ blocks re-init in proxied-server
// mode. We deliberately don't peek deeper than the directory itself — the
// internal layout (wrapper db dir, per-db subdirs) is an implementation
// detail of the daemon.
func TestCheckExistingBeadsDataAt_ProxiedServerWithExistingDB(t *testing.T) {
	beadsDir := filepath.Join(t.TempDir(), ".beads")
	require.NoError(t, os.MkdirAll(beadsDir, 0o755))

	metadata := map[string]interface{}{
		"database":      "dolt",
		"backend":       "dolt",
		"dolt_mode":     "proxied-server",
		"dolt_database": "myproj",
	}
	data, err := json.Marshal(metadata)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(beadsDir, "metadata.json"), data, 0o644))

	// Materialize <beadsDir>/proxieddb/ — that alone should be enough to
	// trip the guard, regardless of what's inside.
	proxiedRoot := filepath.Join(beadsDir, "proxieddb")
	require.NoError(t, os.MkdirAll(proxiedRoot, 0o755))

	err = checkExistingBeadsDataAt(beadsDir, "myproj")
	require.Error(t, err, "existing proxieddb directory should block init")
	assert.Contains(t, err.Error(), "already initialized")
	assert.Contains(t, err.Error(), proxiedRoot)
}
