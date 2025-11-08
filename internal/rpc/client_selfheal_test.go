package rpc

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCleanupStaleDaemonArtifacts(t *testing.T) {
	// Create temp directory for test
	tmpDir := t.TempDir()
	beadsDir := filepath.Join(tmpDir, ".beads")
	if err := os.MkdirAll(beadsDir, 0755); err != nil {
		t.Fatalf("failed to create beads dir: %v", err)
	}

	pidFile := filepath.Join(beadsDir, "daemon.pid")

	// Test 1: No pid file - should not error
	t.Run("no_pid_file", func(t *testing.T) {
		cleanupStaleDaemonArtifacts(beadsDir)
		// Should not panic or error
	})

	// Test 2: Pid file exists - should be removed
	t.Run("removes_pid_file", func(t *testing.T) {
		// Create stale pid file
		if err := os.WriteFile(pidFile, []byte("12345\n"), 0644); err != nil {
			t.Fatalf("failed to create pid file: %v", err)
		}

		// Verify it exists
		if _, err := os.Stat(pidFile); err != nil {
			t.Fatalf("pid file should exist before cleanup: %v", err)
		}

		// Clean up
		cleanupStaleDaemonArtifacts(beadsDir)

		// Verify it was removed
		if _, err := os.Stat(pidFile); err == nil {
			t.Errorf("pid file should have been removed")
		}
	})

	// Test 3: Permission denied - should not panic
	t.Run("permission_denied", func(t *testing.T) {
		// Create pid file
		if err := os.WriteFile(pidFile, []byte("12345\n"), 0644); err != nil {
			t.Fatalf("failed to create pid file: %v", err)
		}

		// Make directory read-only (on Unix-like systems)
		if err := os.Chmod(beadsDir, 0555); err != nil {
			t.Fatalf("failed to make directory read-only: %v", err)
		}
		defer func() {
			// Restore permissions for cleanup
			_ = os.Chmod(beadsDir, 0755)
		}()

		// Should not panic even if removal fails
		cleanupStaleDaemonArtifacts(beadsDir)
	})
}

func TestTryConnectWithTimeout_SelfHeal(t *testing.T) {
	// Create temp directory for test
	tmpDir := t.TempDir()
	beadsDir := filepath.Join(tmpDir, ".beads")
	if err := os.MkdirAll(beadsDir, 0755); err != nil {
		t.Fatalf("failed to create beads dir: %v", err)
	}

	socketPath := filepath.Join(beadsDir, "bd.sock")
	pidFile := filepath.Join(beadsDir, "daemon.pid")

	// Create stale pid file (no socket, no lock)
	if err := os.WriteFile(pidFile, []byte("99999\n"), 0644); err != nil {
		t.Fatalf("failed to create pid file: %v", err)
	}

	// Verify pid file exists
	if _, err := os.Stat(pidFile); err != nil {
		t.Fatalf("pid file should exist before test: %v", err)
	}

	// Try to connect (should fail but clean up stale pid file)
	client, err := TryConnectWithTimeout(socketPath, 100)
	if client != nil {
		t.Errorf("expected nil client (no daemon running)")
	}
	if err != nil {
		t.Errorf("expected nil error, got: %v", err)
	}

	// Verify pid file was cleaned up
	if _, err := os.Stat(pidFile); err == nil {
		t.Errorf("pid file should have been removed during self-heal")
	}
}
