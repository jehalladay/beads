package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// beads-l9nh: hermetic tests for two file-backed helpers verified 0% + no test
// refs: backupPollutedIssues (detect_pollution.go, writes issues as JSONL) and
// validateBackupRestoreDir (backup_restore.go, dir-existence check).

func TestBackupPollutedIssues(t *testing.T) {
	t.Run("writes one JSONL line per issue", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "polluted.jsonl")
		polluted := []pollutionResult{
			{issue: &types.Issue{ID: "test-1", Title: "test-one"}},
			{issue: &types.Issue{ID: "test-2", Title: "test-two"}},
		}
		if err := backupPollutedIssues(polluted, path); err != nil {
			t.Fatalf("backupPollutedIssues: %v", err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read back: %v", err)
		}
		lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
		if len(lines) != 2 {
			t.Fatalf("expected 2 JSONL lines, got %d:\n%s", len(lines), data)
		}
		var first types.Issue
		if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
			t.Fatalf("line 0 not valid JSON: %v", err)
		}
		if first.ID != "test-1" {
			t.Errorf("line 0 ID = %q, want test-1", first.ID)
		}
	})

	t.Run("empty slice writes an empty file, no error", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "empty.jsonl")
		if err := backupPollutedIssues(nil, path); err != nil {
			t.Fatalf("backupPollutedIssues(nil): %v", err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read back: %v", err)
		}
		if len(data) != 0 {
			t.Errorf("expected empty file, got %q", data)
		}
	})

	t.Run("uncreatable path errors", func(t *testing.T) {
		// A path under a nonexistent directory cannot be created.
		bad := filepath.Join(t.TempDir(), "no-such-dir", "x.jsonl")
		if err := backupPollutedIssues(nil, bad); err == nil {
			t.Error("expected an error creating a file under a missing directory")
		}
	})
}

func TestValidateBackupRestoreDir(t *testing.T) {
	t.Run("existing dir → nil", func(t *testing.T) {
		if err := validateBackupRestoreDir(t.TempDir()); err != nil {
			t.Errorf("existing dir should validate, got %v", err)
		}
	})

	t.Run("missing dir → error mentioning bd backup", func(t *testing.T) {
		missing := filepath.Join(t.TempDir(), "does-not-exist")
		err := validateBackupRestoreDir(missing)
		if err == nil {
			t.Fatal("expected an error for a missing backup dir")
		}
		if !strings.Contains(err.Error(), "backup directory not found") {
			t.Errorf("unexpected error message: %v", err)
		}
	})
}
