//go:build cgo

package doctor

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/configfile"
)

// writeServerModeConfig writes a server-mode metadata.json pointing at host:port
// and returns the tmp town path to pass to CheckFederationRemotesAPI.
func writeServerModeConfig(t *testing.T, mode, host string, port int) string {
	t.Helper()
	tmpDir := t.TempDir()
	beadsDir := filepath.Join(tmpDir, ".beads")
	doltDir := filepath.Join(beadsDir, "dolt")
	if err := os.MkdirAll(doltDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := &configfile.Config{
		Backend:        configfile.BackendDolt,
		DoltMode:       mode,
		DoltServerHost: host,
		DoltServerPort: port,
	}
	data, _ := json.Marshal(cfg)
	if err := os.WriteFile(filepath.Join(beadsDir, "metadata.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	return tmpDir
}

// clearServerModeEnv removes env vars that would override the metadata.json
// server host/port/mode, so the test is hermetic.
func clearServerModeEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"BEADS_DOLT_SERVER_MODE", "BEADS_DOLT_SHARED_SERVER",
		"BEADS_DOLT_SERVER_HOST", "BEADS_DOLT_SERVER_PORT", "BEADS_DOLT_PORT",
		"GT_ROOT",
	} {
		t.Setenv(k, "")
	}
}

// TestCheckFederationRemotesAPI_ServerModeRemoteReachable is the beads-a164
// regression: in server mode with a REACHABLE configured remote, the check must
// report OK (connected to remote) — NOT the old false "Server not running /
// start a sql-server" warning that came from probing only the LOCAL pid file.
func TestCheckFederationRemotesAPI_ServerModeRemoteReachable(t *testing.T) {
	clearServerModeEnv(t)
	old := federationDialTimeout
	federationDialTimeout = 2 * time.Second
	defer func() { federationDialTimeout = old }()

	// Stand up a local TCP listener to act as the "remote" dolt server.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	_, portStr, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(portStr)

	tmpDir := writeServerModeConfig(t, configfile.DoltModeServer, "127.0.0.1", port)
	check := CheckFederationRemotesAPI(tmpDir)

	if check.Status != StatusOK {
		t.Fatalf("server-mode reachable remote: expected StatusOK, got %s: %s", check.Status, check.Message)
	}
	if !strings.Contains(check.Message, "remote dolt server") {
		t.Errorf("expected a 'remote dolt server' message, got: %s", check.Message)
	}
	if strings.Contains(check.Message, "not running") {
		t.Errorf("must NOT report 'not running' for a reachable remote, got: %s", check.Message)
	}
}

// TestCheckFederationRemotesAPI_ServerModeRemoteUnreachable: server mode with an
// UNREACHABLE configured remote reports an honest 'not reachable' ERROR (not the
// misleading 'Server not running — start a sql-server' local-mode warning).
func TestCheckFederationRemotesAPI_ServerModeRemoteUnreachable(t *testing.T) {
	clearServerModeEnv(t)
	old := federationDialTimeout
	federationDialTimeout = 500 * time.Millisecond
	defer func() { federationDialTimeout = old }()

	// Bind then immediately close to get a port that is (almost certainly) closed.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, portStr, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(portStr)
	ln.Close()

	tmpDir := writeServerModeConfig(t, configfile.DoltModeServer, "127.0.0.1", port)
	check := CheckFederationRemotesAPI(tmpDir)

	if check.Status != StatusError {
		t.Fatalf("server-mode unreachable remote: expected StatusError, got %s: %s", check.Status, check.Message)
	}
	if !strings.Contains(check.Message, "not reachable") {
		t.Errorf("expected 'not reachable' message, got: %s", check.Message)
	}
	// The misleading local-mode advice must not be used in server mode.
	if strings.Contains(check.Fix, "Start dolt sql-server in server mode") {
		t.Errorf("server-mode error must not use the local 'start a sql-server' fix, got: %s", check.Fix)
	}
}

// TestCheckFederationRemotesAPI_ProxiedServerModeReachable: the proxied-server
// mode takes the same server-mode-aware path.
func TestCheckFederationRemotesAPI_ProxiedServerModeReachable(t *testing.T) {
	clearServerModeEnv(t)
	old := federationDialTimeout
	federationDialTimeout = 2 * time.Second
	defer func() { federationDialTimeout = old }()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	_, portStr, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(portStr)

	tmpDir := writeServerModeConfig(t, configfile.DoltModeProxiedServer, "127.0.0.1", port)
	check := CheckFederationRemotesAPI(tmpDir)

	if check.Status != StatusOK {
		t.Fatalf("proxied-server-mode reachable remote: expected StatusOK, got %s: %s", check.Status, check.Message)
	}
}
