package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/steveyegge/beads/internal/configfile"
)

// beads-aywz: hermetic tests for inferPrefix (bootstrap.go) and formatViperValue
// (config_show.go). Both verified 0% + no test references.

func TestInferPrefix(t *testing.T) {
	// GetDoltDatabase consults BEADS_DOLT_SERVER_DATABASE first; clear it so the
	// test drives the config/cwd logic, not crew-shell env pollution.
	t.Setenv("BEADS_DOLT_SERVER_DATABASE", "")

	t.Run("configured non-default db is used as the prefix", func(t *testing.T) {
		cfg := &configfile.Config{DoltDatabase: "myproject"}
		if got := inferPrefix(cfg); got != "myproject" {
			t.Errorf("inferPrefix = %q, want myproject", got)
		}
	})

	t.Run("default 'beads' db falls back to cwd basename", func(t *testing.T) {
		dir := t.TempDir()
		leaf := filepath.Join(dir, "coolrepo")
		if err := os.MkdirAll(leaf, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		t.Chdir(leaf)
		cfg := &configfile.Config{DoltDatabase: "beads"}
		if got := inferPrefix(cfg); got != "coolrepo" {
			t.Errorf("inferPrefix = %q, want coolrepo (cwd basename)", got)
		}
	})

	t.Run("empty db also falls back to cwd basename", func(t *testing.T) {
		dir := t.TempDir()
		leaf := filepath.Join(dir, "widgets")
		if err := os.MkdirAll(leaf, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		t.Chdir(leaf)
		cfg := &configfile.Config{} // GetDoltDatabase returns the default "beads"
		if got := inferPrefix(cfg); got != "widgets" {
			t.Errorf("inferPrefix = %q, want widgets", got)
		}
	})
}

func TestFormatViperValue(t *testing.T) {
	if got := formatViperValue(""); got != "" {
		t.Errorf("empty → %q, want empty", got)
	}
	if got := formatViperValue("hello"); got != "hello" {
		t.Errorf("non-empty passthrough → %q, want hello", got)
	}
}
