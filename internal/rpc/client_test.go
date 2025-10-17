package rpc

import (
	"path/filepath"
	"testing"
	"time"
)

func TestTryConnectNoSocket(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "nonexistent.sock")

	client := TryConnect(sockPath)
	if client != nil {
		t.Error("Expected nil client when socket doesn't exist")
	}
}

func TestTryConnectSuccess(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "test.sock")

	store := &mockStorage{}
	server := NewServer(store, sockPath)

	if err := server.Start(); err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}
	defer server.Stop()

	time.Sleep(100 * time.Millisecond)

	client := TryConnect(sockPath)
	if client == nil {
		t.Fatal("Expected client to connect successfully")
	}
	defer client.Close()

	if client.sockPath != sockPath {
		t.Errorf("Expected sockPath %s, got %s", sockPath, client.sockPath)
	}
}

func TestClientExecute(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "test.sock")

	store := &mockStorage{}
	server := NewServer(store, sockPath)

	if err := server.Start(); err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}
	defer server.Stop()

	time.Sleep(100 * time.Millisecond)

	client := TryConnect(sockPath)
	if client == nil {
		t.Fatal("Failed to connect to server")
	}
	defer client.Close()

	req, _ := NewRequest(OpList, map[string]string{"status": "open"})
	resp, err := client.Execute(req)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if resp.Success {
		t.Error("Expected error response for unimplemented operation")
	}
}

func TestClientMultipleRequests(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "test.sock")

	store := &mockStorage{}
	server := NewServer(store, sockPath)

	if err := server.Start(); err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}
	defer server.Stop()

	time.Sleep(100 * time.Millisecond)

	client := TryConnect(sockPath)
	if client == nil {
		t.Fatal("Failed to connect to server")
	}
	defer client.Close()

	for i := 0; i < 5; i++ {
		req, _ := NewRequest(OpStats, nil)
		resp, err := client.Execute(req)
		if err != nil {
			t.Fatalf("Execute %d failed: %v", i, err)
		}
		if resp == nil {
			t.Fatalf("Execute %d returned nil response", i)
		}
	}
}

func TestSocketPath(t *testing.T) {
	beadsDir := "/home/user/project/.beads"
	expected := "/home/user/project/.beads/bd.sock"

	result := SocketPath(beadsDir)
	if result != expected {
		t.Errorf("Expected %s, got %s", expected, result)
	}
}
