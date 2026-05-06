package doltserver

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	_ "github.com/go-sql-driver/mysql"

	"github.com/steveyegge/beads/internal/storage/db/proxy"
	"github.com/steveyegge/beads/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewDoltServerStore_ValidationErrors(t *testing.T) {
	cases := []struct {
		name     string
		database string
		rootUser string
		doltBin  string
		backend  proxy.Backend
		want     string
	}{
		{"empty database", "", "root", "/usr/bin/true", proxy.BackendLocalServer, "database name must not be empty"},
		{"invalid backend", "beads", "root", "/usr/bin/true", proxy.Backend("nope"), "unknown backend"},
		{"empty rootUser", "beads", "", "/usr/bin/true", proxy.BackendLocalServer, "rootUser must not be empty"},
		{"empty doltBin", "beads", "root", "", proxy.BackendLocalServer, "doltBinExec must not be empty"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, err := newDoltServerStore(
				context.Background(),
				t.TempDir(), t.TempDir(),
				tc.database, "Test", "test@example.com",
				"", "", tc.backend, false,
				tc.rootUser, "", tc.doltBin,
			)
			assert.Nil(t, s)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestNewDoltServerStore_HappyPath(t *testing.T) {
	testutil.RequireDoltBinary(t)
	bin, err := exec.LookPath("dolt")
	require.NoError(t, err)

	bdBin := buildBDBinary(t)
	prev := proxy.ResolveExecutable
	proxy.ResolveExecutable = func() (string, error) { return bdBin, nil }
	t.Cleanup(func() { proxy.ResolveExecutable = prev })

	t.Setenv("HOME", t.TempDir())

	port, err := proxy.PickFreePort()
	require.NoError(t, err)
	storeRootDir := t.TempDir()
	cfgPath := writeServerConfig(t, port)
	logPath := filepath.Join(t.TempDir(), "server.log")

	store, err := newDoltServerStore(
		context.Background(),
		storeRootDir,
		t.TempDir(),
		"beads",
		"test_user",
		"test@example.com",
		logPath,
		cfgPath,
		proxy.BackendLocalServer,
		false,
		"root",
		"",
		bin,
	)

	require.NoError(t, err)
	require.NotNil(t, store)
	t.Cleanup(func() { _ = store.db.Close() })
}

var (
	bdBinaryOnce sync.Once
	bdBinary     string
	bdBinaryErr  error
)

func buildBDBinary(t *testing.T) string {
	t.Helper()
	bdBinaryOnce.Do(func() {
		if prebuilt := os.Getenv("BEADS_TEST_BD_BINARY"); prebuilt != "" {
			if _, err := os.Stat(prebuilt); err != nil {
				bdBinaryErr = fmt.Errorf("BEADS_TEST_BD_BINARY=%q not found: %w", prebuilt, err)
				return
			}
			bdBinary = prebuilt
			return
		}
		tmpDir, err := os.MkdirTemp("", "bd-doltserver-test-*")
		if err != nil {
			bdBinaryErr = fmt.Errorf("temp dir: %w", err)
			return
		}
		name := "bd"
		if runtime.GOOS == "windows" {
			name = "bd.exe"
		}
		bdBinary = filepath.Join(tmpDir, name)
		cmd := exec.Command("go", "build", "-tags", "gms_pure_go", "-o", bdBinary, "github.com/steveyegge/beads/cmd/bd")
		if out, err := cmd.CombinedOutput(); err != nil {
			bdBinaryErr = fmt.Errorf("go build bd: %v\n%s", err, out)
		}
	})
	if bdBinaryErr != nil {
		t.Fatalf("build bd: %v", bdBinaryErr)
	}
	return bdBinary
}

func writeServerConfig(t *testing.T, port int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	body := fmt.Sprintf("log_level: debug\nlistener:\n  host: 127.0.0.1\n  port: %d\n", port)
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}
