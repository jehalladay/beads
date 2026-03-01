package fix

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/steveyegge/beads/internal/configfile"
)

// TestDatabaseConfigFix_DBMismatch tests that DatabaseConfig fixes database path mismatches.
func TestDatabaseConfigFix_DBMismatch(t *testing.T) {
	tmpDir := t.TempDir()
	beadsDir := filepath.Join(tmpDir, ".beads")
	if err := os.Mkdir(beadsDir, 0755); err != nil {
		t.Fatalf("Failed to create .beads dir: %v", err)
	}

	// Create actual database file
	dbPath := filepath.Join(beadsDir, "beads.db")
	if err := os.WriteFile(dbPath, []byte("test"), 0600); err != nil {
		t.Fatalf("Failed to create db: %v", err)
	}

	// Create metadata.json pointing to wrong database (SQLite backend)
	cfg := &configfile.Config{
		Database: "wrong.db",
		Backend:  configfile.BackendSQLite,
	}
	if err := cfg.Save(beadsDir); err != nil {
		t.Fatalf("Failed to save config: %v", err)
	}

	// Run the fix
	if err := DatabaseConfig(tmpDir); err != nil {
		t.Fatalf("DatabaseConfig failed: %v", err)
	}

	// Verify the config was updated
	updatedCfg, err := configfile.Load(beadsDir)
	if err != nil {
		t.Fatalf("Failed to load updated config: %v", err)
	}

	if updatedCfg.Database != "beads.db" {
		t.Errorf("Expected Database to be 'beads.db', got %q", updatedCfg.Database)
	}
}

// TestDatabaseConfigFix_NoMismatch tests that DatabaseConfig returns error when no mismatch exists.
func TestDatabaseConfigFix_NoMismatch(t *testing.T) {
	tmpDir := t.TempDir()
	beadsDir := filepath.Join(tmpDir, ".beads")
	if err := os.Mkdir(beadsDir, 0755); err != nil {
		t.Fatalf("Failed to create .beads dir: %v", err)
	}

	// Create database matching config
	dbPath := filepath.Join(beadsDir, "beads.db")
	if err := os.WriteFile(dbPath, []byte("test"), 0600); err != nil {
		t.Fatalf("Failed to create db: %v", err)
	}

	cfg := &configfile.Config{
		Database: "beads.db",
		Backend:  configfile.BackendSQLite,
	}
	if err := cfg.Save(beadsDir); err != nil {
		t.Fatalf("Failed to save config: %v", err)
	}

	err := DatabaseConfig(tmpDir)
	if err == nil {
		t.Error("Expected error when no mismatch detected")
	}
}

// TestDatabaseConfigFix_DoltBackend tests that DatabaseConfig returns a clear error for Dolt backends.
func TestDatabaseConfigFix_DoltBackend(t *testing.T) {
	tmpDir := t.TempDir()
	beadsDir := filepath.Join(tmpDir, ".beads")
	if err := os.Mkdir(beadsDir, 0755); err != nil {
		t.Fatalf("Failed to create .beads dir: %v", err)
	}

	// Create config with Dolt backend (the default and most common case)
	cfg := &configfile.Config{
		Backend:  configfile.BackendDolt,
		Database: "dolt",
	}
	if err := cfg.Save(beadsDir); err != nil {
		t.Fatalf("Failed to save config: %v", err)
	}

	err := DatabaseConfig(tmpDir)
	if err == nil {
		t.Fatal("Expected error for Dolt backend, got nil")
	}
	if got := err.Error(); got != "database config fix not applicable for Dolt backend (data is on the server)" {
		t.Errorf("Unexpected error message: %q", got)
	}
}

// TestDatabaseConfigFix_DoltBackendDefault tests that empty backend (defaults to Dolt) is handled.
func TestDatabaseConfigFix_DoltBackendDefault(t *testing.T) {
	tmpDir := t.TempDir()
	beadsDir := filepath.Join(tmpDir, ".beads")
	if err := os.Mkdir(beadsDir, 0755); err != nil {
		t.Fatalf("Failed to create .beads dir: %v", err)
	}

	// Empty backend defaults to Dolt
	cfg := &configfile.Config{
		Database: "",
	}
	if err := cfg.Save(beadsDir); err != nil {
		t.Fatalf("Failed to save config: %v", err)
	}

	err := DatabaseConfig(tmpDir)
	if err == nil {
		t.Fatal("Expected error for default (Dolt) backend, got nil")
	}
	if got := err.Error(); got != "database config fix not applicable for Dolt backend (data is on the server)" {
		t.Errorf("Unexpected error message: %q", got)
	}
}

// TestFindActualDBFile tests database file discovery.
func TestFindActualDBFile(t *testing.T) {
	t.Run("finds beads.db", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "beads.db"), []byte("test"), 0600); err != nil {
			t.Fatal(err)
		}
		if got := findActualDBFile(dir); got != "beads.db" {
			t.Errorf("expected beads.db, got %q", got)
		}
	})

	t.Run("skips backups", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "beads.db.backup"), []byte("test"), 0600); err != nil {
			t.Fatal(err)
		}
		if got := findActualDBFile(dir); got != "" {
			t.Errorf("expected empty, got %q", got)
		}
	})

	t.Run("skips vc.db", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "vc.db"), []byte("test"), 0600); err != nil {
			t.Fatal(err)
		}
		if got := findActualDBFile(dir); got != "" {
			t.Errorf("expected empty, got %q", got)
		}
	})

	t.Run("empty dir", func(t *testing.T) {
		dir := t.TempDir()
		if got := findActualDBFile(dir); got != "" {
			t.Errorf("expected empty, got %q", got)
		}
	})
}
