package rpc

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/types"
)

type mockStorage struct{}

func (m *mockStorage) CreateIssue(ctx context.Context, issue *types.Issue, actor string) error {
	return nil
}
func (m *mockStorage) CreateIssues(ctx context.Context, issues []*types.Issue, actor string) error {
	return nil
}
func (m *mockStorage) GetIssue(ctx context.Context, id string) (*types.Issue, error) { return nil, nil }
func (m *mockStorage) UpdateIssue(ctx context.Context, id string, updates map[string]interface{}, actor string) error {
	return nil
}
func (m *mockStorage) CloseIssue(ctx context.Context, id, reason, actor string) error { return nil }
func (m *mockStorage) SearchIssues(ctx context.Context, query string, filter types.IssueFilter) ([]*types.Issue, error) {
	return nil, nil
}
func (m *mockStorage) AddDependency(ctx context.Context, dep *types.Dependency, actor string) error {
	return nil
}
func (m *mockStorage) RemoveDependency(ctx context.Context, issueID, dependsOnID, actor string) error {
	return nil
}
func (m *mockStorage) GetDependencies(ctx context.Context, issueID string) ([]*types.Issue, error) {
	return nil, nil
}
func (m *mockStorage) GetDependents(ctx context.Context, issueID string) ([]*types.Issue, error) {
	return nil, nil
}
func (m *mockStorage) GetDependencyRecords(ctx context.Context, issueID string) ([]*types.Dependency, error) {
	return nil, nil
}
func (m *mockStorage) GetAllDependencyRecords(ctx context.Context) (map[string][]*types.Dependency, error) {
	return nil, nil
}
func (m *mockStorage) GetDependencyTree(ctx context.Context, issueID string, maxDepth int) ([]*types.TreeNode, error) {
	return nil, nil
}
func (m *mockStorage) DetectCycles(ctx context.Context) ([][]*types.Issue, error) { return nil, nil }
func (m *mockStorage) AddLabel(ctx context.Context, issueID, label, actor string) error {
	return nil
}
func (m *mockStorage) RemoveLabel(ctx context.Context, issueID, label, actor string) error {
	return nil
}
func (m *mockStorage) GetLabels(ctx context.Context, issueID string) ([]string, error) {
	return nil, nil
}
func (m *mockStorage) GetIssuesByLabel(ctx context.Context, label string) ([]*types.Issue, error) {
	return nil, nil
}
func (m *mockStorage) GetReadyWork(ctx context.Context, filter types.WorkFilter) ([]*types.Issue, error) {
	return nil, nil
}
func (m *mockStorage) GetBlockedIssues(ctx context.Context) ([]*types.BlockedIssue, error) {
	return nil, nil
}
func (m *mockStorage) AddComment(ctx context.Context, issueID, actor, comment string) error {
	return nil
}
func (m *mockStorage) GetEvents(ctx context.Context, issueID string, limit int) ([]*types.Event, error) {
	return nil, nil
}
func (m *mockStorage) GetStatistics(ctx context.Context) (*types.Statistics, error) {
	return nil, nil
}
func (m *mockStorage) GetDirtyIssues(ctx context.Context) ([]string, error) { return nil, nil }
func (m *mockStorage) ClearDirtyIssues(ctx context.Context) error           { return nil }
func (m *mockStorage) ClearDirtyIssuesByID(ctx context.Context, issueIDs []string) error {
	return nil
}
func (m *mockStorage) SetConfig(ctx context.Context, key, value string) error { return nil }
func (m *mockStorage) GetConfig(ctx context.Context, key string) (string, error) {
	return "", nil
}
func (m *mockStorage) SetMetadata(ctx context.Context, key, value string) error { return nil }
func (m *mockStorage) GetMetadata(ctx context.Context, key string) (string, error) {
	return "", nil
}
func (m *mockStorage) UpdateIssueID(ctx context.Context, oldID, newID string, issue *types.Issue, actor string) error {
	return nil
}
func (m *mockStorage) RenameDependencyPrefix(ctx context.Context, oldPrefix, newPrefix string) error {
	return nil
}
func (m *mockStorage) RenameCounterPrefix(ctx context.Context, oldPrefix, newPrefix string) error {
	return nil
}
func (m *mockStorage) Close() error { return nil }

func TestServerStartStop(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "test.sock")

	store := &mockStorage{}
	server := NewServer(store, sockPath)

	if err := server.Start(); err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}

	if _, err := os.Stat(sockPath); os.IsNotExist(err) {
		t.Fatal("Socket file was not created")
	}

	if err := server.Stop(); err != nil {
		t.Fatalf("Failed to stop server: %v", err)
	}

	if _, err := os.Stat(sockPath); !os.IsNotExist(err) {
		t.Fatal("Socket file was not removed")
	}
}

func TestServerHandlesRequest(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "test.sock")

	store := &mockStorage{}
	server := NewServer(store, sockPath)

	if err := server.Start(); err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}
	defer server.Stop()

	time.Sleep(100 * time.Millisecond)

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("Failed to connect to server: %v", err)
	}
	defer conn.Close()

	req, _ := NewRequest(OpStats, nil)
	reqJSON, _ := json.Marshal(req)
	reqJSON = append(reqJSON, '\n')

	if _, err := conn.Write(reqJSON); err != nil {
		t.Fatalf("Failed to write request: %v", err)
	}

	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("Failed to read response: %v", err)
	}

	var resp Response
	if err := json.Unmarshal(buf[:n], &resp); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	if resp.Success {
		t.Error("Expected error response for unimplemented operation")
	}
}

func TestServerRejectsInvalidJSON(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "test.sock")

	store := &mockStorage{}
	server := NewServer(store, sockPath)

	if err := server.Start(); err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}
	defer server.Stop()

	time.Sleep(100 * time.Millisecond)

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("Failed to connect to server: %v", err)
	}
	defer conn.Close()

	if _, err := conn.Write([]byte("invalid json\n")); err != nil {
		t.Fatalf("Failed to write request: %v", err)
	}

	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("Failed to read response: %v", err)
	}

	var resp Response
	if err := json.Unmarshal(buf[:n], &resp); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	if resp.Success {
		t.Error("Expected error response for invalid JSON")
	}

	if resp.Error == "" {
		t.Error("Expected error message")
	}
}
