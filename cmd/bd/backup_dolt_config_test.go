package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// beads-7cws: hermetic round-trip tests for backup_dolt.go config/state helpers
// (verified 0% + no test refs). All resolve their path via beads.FindBeadsDir(),
// so a t.TempDir .beads with a metadata.json + BEADS_DIR env drives them without
// a DB.

// setupBackupBeadsDir creates a .beads dir with a metadata.json (so FindBeadsDir
// accepts it) and points BEADS_DIR at it. Returns the dir.
func setupBackupBeadsDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), ".beads")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir .beads: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "metadata.json"), []byte("{}"), 0o644); err != nil {
		t.Fatalf("write metadata.json: %v", err)
	}
	t.Setenv("BEADS_DIR", dir)
	return dir
}

func TestDoltBackupPaths(t *testing.T) {
	dir := setupBackupBeadsDir(t)

	cfgPath, err := doltBackupConfigPath()
	if err != nil {
		t.Fatalf("doltBackupConfigPath: %v", err)
	}
	if cfgPath != filepath.Join(dir, "dolt-backup.json") {
		t.Errorf("config path = %q", cfgPath)
	}

	statePath, err := doltBackupStatePath()
	if err != nil {
		t.Fatalf("doltBackupStatePath: %v", err)
	}
	if statePath != filepath.Join(dir, "dolt-backup-state.json") {
		t.Errorf("state path = %q", statePath)
	}
}

func TestDoltBackupConfig_RoundTrip(t *testing.T) {
	setupBackupBeadsDir(t)

	// No config yet → load returns (nil, nil).
	got, err := loadDoltBackupConfig()
	if err != nil {
		t.Fatalf("load (absent): %v", err)
	}
	if got != nil {
		t.Errorf("expected nil config when none saved, got %+v", got)
	}

	// Save then load.
	if err := saveDoltBackupConfig("file:///tmp/backup-remote"); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err = loadDoltBackupConfig()
	if err != nil {
		t.Fatalf("load (present): %v", err)
	}
	if got == nil || got.BackupURL != "file:///tmp/backup-remote" {
		t.Fatalf("round-trip lost BackupURL: %+v", got)
	}
	if got.BackupName != defaultDoltBackupName {
		t.Errorf("BackupName = %q, want %q", got.BackupName, defaultDoltBackupName)
	}
	if got.CreatedAt.IsZero() {
		t.Error("CreatedAt should be set")
	}
}

func TestDoltBackupState_RoundTrip(t *testing.T) {
	setupBackupBeadsDir(t)

	// No state yet → (nil, nil).
	got, err := loadDoltBackupState()
	if err != nil {
		t.Fatalf("load (absent): %v", err)
	}
	if got != nil {
		t.Errorf("expected nil state when none saved, got %+v", got)
	}

	// Update then load.
	if err := updateDoltBackupState(1500 * time.Millisecond); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, err = loadDoltBackupState()
	if err != nil {
		t.Fatalf("load (present): %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil state after update")
	}
	if got.LastSync.IsZero() {
		t.Error("LastSync should be set")
	}
	if got.Duration != (1500 * time.Millisecond).String() {
		t.Errorf("Duration = %q, want %q", got.Duration, (1500 * time.Millisecond).String())
	}
}

func TestDoltBackupPaths_NoWorkspace(t *testing.T) {
	// BEADS_DIR empty + cwd is an isolated tempdir with no .beads anywhere up the
	// tree → FindBeadsDir returns "" → the path helpers error.
	t.Setenv("BEADS_DIR", "")
	t.Setenv("BEADS_DB", "")
	t.Chdir(t.TempDir())
	if _, err := doltBackupConfigPath(); err == nil {
		t.Error("expected error when no active workspace")
	}
	if _, err := doltBackupStatePath(); err == nil {
		t.Error("expected error when no active workspace")
	}
	// save/update propagate the path error.
	if err := saveDoltBackupConfig("file:///x"); err == nil {
		t.Error("saveDoltBackupConfig should propagate the no-workspace error")
	}
	if err := updateDoltBackupState(time.Second); err == nil {
		t.Error("updateDoltBackupState should propagate the no-workspace error")
	}
}

func TestDoltBackupConfig_CorruptJSON(t *testing.T) {
	setupBackupBeadsDir(t)
	path, err := doltBackupConfigPath()
	if err != nil {
		t.Fatalf("path: %v", err)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write corrupt: %v", err)
	}
	if _, err := loadDoltBackupConfig(); err == nil {
		t.Error("expected an unmarshal error for corrupt config JSON")
	}
}
