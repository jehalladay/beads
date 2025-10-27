package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/steveyegge/beads/internal/rpc"
	"github.com/steveyegge/beads/internal/storage/sqlite"
)

func TestDiscoverDaemon(t *testing.T) {
	tmpDir := t.TempDir()
	workspace := filepath.Join(tmpDir, ".beads")
	os.MkdirAll(workspace, 0755)

	// Start daemon
	dbPath := filepath.Join(workspace, "test.db")
	socketPath := filepath.Join(workspace, "bd.sock")
	store, err := sqlite.New(dbPath)
	if err != nil {
		t.Fatalf("failed to create storage: %v", err)
	}
	defer store.Close()

	server := rpc.NewServer(socketPath, store, tmpDir, dbPath)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go server.Start(ctx)
	<-server.WaitReady()
	defer server.Stop()

	// Test discoverDaemon directly
	daemon := discoverDaemon(socketPath)
	if !daemon.Alive {
		t.Errorf("daemon not alive: %s", daemon.Error)
	}
	if daemon.PID != os.Getpid() {
		t.Errorf("wrong PID: expected %d, got %d", os.Getpid(), daemon.PID)
	}
	if daemon.UptimeSeconds <= 0 {
		t.Errorf("invalid uptime: %f", daemon.UptimeSeconds)
	}
	if daemon.WorkspacePath != tmpDir {
		t.Errorf("wrong workspace: expected %s, got %s", tmpDir, daemon.WorkspacePath)
	}
}

func TestFindDaemonByWorkspace(t *testing.T) {
	tmpDir := t.TempDir()
	workspace := filepath.Join(tmpDir, ".beads")
	os.MkdirAll(workspace, 0755)

	// Start daemon
	dbPath := filepath.Join(workspace, "test.db")
	socketPath := filepath.Join(workspace, "bd.sock")
	store, err := sqlite.New(dbPath)
	if err != nil {
		t.Fatalf("failed to create storage: %v", err)
	}
	defer store.Close()

	server := rpc.NewServer(socketPath, store, tmpDir, dbPath)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go server.Start(ctx)
	<-server.WaitReady()
	defer server.Stop()

	// Find daemon by workspace
	daemon, err := FindDaemonByWorkspace(tmpDir)
	if err != nil {
		t.Fatalf("failed to find daemon: %v", err)
	}
	if daemon == nil {
		t.Fatal("daemon not found")
	}
	if !daemon.Alive {
		t.Errorf("daemon not alive: %s", daemon.Error)
	}
	if daemon.WorkspacePath != tmpDir {
		t.Errorf("wrong workspace: expected %s, got %s", tmpDir, daemon.WorkspacePath)
	}
}

func TestCleanupStaleSockets(t *testing.T) {
	tmpDir := t.TempDir()

	// Create stale socket file
	stalePath := filepath.Join(tmpDir, "stale.sock")
	if err := os.WriteFile(stalePath, []byte{}, 0644); err != nil {
		t.Fatalf("failed to create stale socket: %v", err)
	}

	daemons := []DaemonInfo{
		{
			SocketPath: stalePath,
			Alive:      false,
		},
	}

	cleaned, err := CleanupStaleSockets(daemons)
	if err != nil {
		t.Fatalf("cleanup failed: %v", err)
	}
	if cleaned != 1 {
		t.Errorf("expected 1 cleaned, got %d", cleaned)
	}

	// Verify socket was removed
	if _, err := os.Stat(stalePath); !os.IsNotExist(err) {
		t.Error("stale socket still exists")
	}
}
