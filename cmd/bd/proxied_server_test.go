package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dolthub/dolt/go/libraries/doltcore/servercfg"
	"github.com/dolthub/dolt/go/libraries/utils/filesys"
	"github.com/steveyegge/beads/internal/configfile"
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

	path1, err := ensureProxiedServerConfig(beadsDir, nil)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(beadsDir, "proxieddb", "server_config.yaml"), path1)

	body1, err := os.ReadFile(path1)
	require.NoError(t, err)
	require.NotEmpty(t, body1)
	require.True(t, strings.Contains(string(body1), proxiedServerListenerHost))

	// Second call must NOT rewrite — running daemon is bound to the existing port.
	path2, err := ensureProxiedServerConfig(beadsDir, nil)
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

// TestInitCommandRegistersServerConfigFlag verifies the --server-config flag
// is wired into initCmd.
func TestInitCommandRegistersServerConfigFlag(t *testing.T) {
	flag := initCmd.Flags().Lookup("server-config")
	require.NotNil(t, flag, "init command does not register --server-config")
	assert.Equal(t, "", flag.DefValue, "--server-config should default to empty")
}

// TestResolveProxiedServerConfigPath covers the env > field-relative >
// field-absolute > default chain.
func TestResolveProxiedServerConfigPath(t *testing.T) {
	t.Run("nil cfg, no env, returns default and !isCustom", func(t *testing.T) {
		t.Setenv("BEADS_PROXIED_SERVER_CONFIG", "")
		bd := t.TempDir()
		path, isCustom := resolveProxiedServerConfigPath(bd, nil)
		assert.Equal(t, proxiedServerConfigPath(bd), path)
		assert.False(t, isCustom)
	})

	t.Run("empty cfg, no env, returns default and !isCustom", func(t *testing.T) {
		t.Setenv("BEADS_PROXIED_SERVER_CONFIG", "")
		bd := t.TempDir()
		path, isCustom := resolveProxiedServerConfigPath(bd, &configfile.Config{})
		assert.Equal(t, proxiedServerConfigPath(bd), path)
		assert.False(t, isCustom)
	})

	t.Run("field relative joins beadsDir and isCustom", func(t *testing.T) {
		t.Setenv("BEADS_PROXIED_SERVER_CONFIG", "")
		bd := t.TempDir()
		cfg := &configfile.Config{DoltProxiedServerConfig: "configs/server.yaml"}
		path, isCustom := resolveProxiedServerConfigPath(bd, cfg)
		assert.Equal(t, filepath.Join(bd, "configs/server.yaml"), path)
		assert.True(t, isCustom)
	})

	t.Run("field absolute returned as-is and isCustom", func(t *testing.T) {
		t.Setenv("BEADS_PROXIED_SERVER_CONFIG", "")
		bd := t.TempDir()
		cfg := &configfile.Config{DoltProxiedServerConfig: "/etc/dolt/server.yaml"}
		path, isCustom := resolveProxiedServerConfigPath(bd, cfg)
		assert.Equal(t, "/etc/dolt/server.yaml", path)
		assert.True(t, isCustom)
	})

	t.Run("env beats field and isCustom", func(t *testing.T) {
		t.Setenv("BEADS_PROXIED_SERVER_CONFIG", "/from/env.yaml")
		bd := t.TempDir()
		cfg := &configfile.Config{DoltProxiedServerConfig: "configs/from-meta.yaml"}
		path, isCustom := resolveProxiedServerConfigPath(bd, cfg)
		assert.Equal(t, "/from/env.yaml", path)
		assert.True(t, isCustom)
	})

	t.Run("env with nil cfg still wins", func(t *testing.T) {
		t.Setenv("BEADS_PROXIED_SERVER_CONFIG", "/from/env.yaml")
		bd := t.TempDir()
		path, isCustom := resolveProxiedServerConfigPath(bd, nil)
		assert.Equal(t, "/from/env.yaml", path)
		assert.True(t, isCustom)
	})
}

// writeValidServerYAML writes a minimal valid dolt sql-server YAML to path
// and returns the path. Used to exercise the custom-config success path.
func writeValidServerYAML(t *testing.T, path string) string {
	t.Helper()
	body, err := renderProxiedServerConfig(54321)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, body, 0o600))
	return path
}

// TestEnsureProxiedServerConfig_CustomPathExists asserts that when a custom
// path is configured, ensureProxiedServerConfig returns it unchanged AND does
// not auto-create the default <beadsDir>/proxieddb/server_config.yaml.
func TestEnsureProxiedServerConfig_CustomPathExists(t *testing.T) {
	t.Setenv("BEADS_PROXIED_SERVER_CONFIG", "")
	bd := t.TempDir()

	customDir := t.TempDir()
	customPath := writeValidServerYAML(t, filepath.Join(customDir, "my-server.yaml"))

	cfg := &configfile.Config{DoltProxiedServerConfig: customPath}
	got, err := ensureProxiedServerConfig(bd, cfg)
	require.NoError(t, err)
	assert.Equal(t, customPath, got)

	defaultPath := proxiedServerConfigPath(bd)
	_, statErr := os.Stat(defaultPath)
	assert.True(t, os.IsNotExist(statErr), "default config must not be auto-created when a custom path is configured (got err=%v)", statErr)
}

// TestEnsureProxiedServerConfig_CustomPathMissing asserts a clear error when
// the user-supplied path doesn't exist. bd never auto-creates user files.
func TestEnsureProxiedServerConfig_CustomPathMissing(t *testing.T) {
	t.Setenv("BEADS_PROXIED_SERVER_CONFIG", "")
	bd := t.TempDir()
	missing := filepath.Join(t.TempDir(), "does-not-exist.yaml")

	cfg := &configfile.Config{DoltProxiedServerConfig: missing}
	_, err := ensureProxiedServerConfig(bd, cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), missing)
}

// TestEnsureProxiedServerConfig_CustomPathInvalidYAML asserts that a
// non-parsable YAML at the custom path is rejected up front rather than
// crashing the daemon downstream.
func TestEnsureProxiedServerConfig_CustomPathInvalidYAML(t *testing.T) {
	t.Setenv("BEADS_PROXIED_SERVER_CONFIG", "")
	bd := t.TempDir()
	bad := filepath.Join(t.TempDir(), "bad.yaml")
	// Unclosed flow sequence — guaranteed YAML parse error.
	require.NoError(t, os.WriteFile(bad, []byte("listener: [host: 127.0.0.1\n"), 0o600))

	cfg := &configfile.Config{DoltProxiedServerConfig: bad}
	_, err := ensureProxiedServerConfig(bd, cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), bad)
	assert.Contains(t, strings.ToLower(err.Error()), "parse")
}

// TestEnsureProxiedServerConfig_CustomPathIsDirectory asserts that pointing
// the custom path at a directory (or other non-regular file) is rejected.
func TestEnsureProxiedServerConfig_CustomPathIsDirectory(t *testing.T) {
	t.Setenv("BEADS_PROXIED_SERVER_CONFIG", "")
	bd := t.TempDir()
	dir := t.TempDir()

	cfg := &configfile.Config{DoltProxiedServerConfig: dir}
	_, err := ensureProxiedServerConfig(bd, cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), dir)
	assert.Contains(t, err.Error(), "not a regular file")
}

// TestValidateProxiedServerConfig covers the standalone validator that
// init.go uses for early --server-config validation.
func TestValidateProxiedServerConfig(t *testing.T) {
	t.Run("valid YAML passes", func(t *testing.T) {
		path := writeValidServerYAML(t, filepath.Join(t.TempDir(), "ok.yaml"))
		require.NoError(t, validateProxiedServerConfig(path))
	})
	t.Run("missing path errors", func(t *testing.T) {
		err := validateProxiedServerConfig(filepath.Join(t.TempDir(), "nope.yaml"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "--server-config")
	})
	t.Run("directory rejected", func(t *testing.T) {
		err := validateProxiedServerConfig(t.TempDir())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not a regular file")
	})
	t.Run("invalid YAML rejected", func(t *testing.T) {
		bad := filepath.Join(t.TempDir(), "bad.yaml")
		require.NoError(t, os.WriteFile(bad, []byte("listener: [host: 127.0.0.1\n"), 0o600))
		err := validateProxiedServerConfig(bad)
		require.Error(t, err)
		assert.Contains(t, strings.ToLower(err.Error()), "parse")
	})
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
