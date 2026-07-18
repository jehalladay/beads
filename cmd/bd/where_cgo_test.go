//go:build cgo

package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/steveyegge/beads/internal/configfile"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/storage/embeddeddolt"
	"github.com/steveyegge/beads/internal/utils"
)

func TestWhereCommand_ReadsPrefixFromEmbeddedStore(t *testing.T) {
	saveAndRestoreGlobals(t)
	ensureCleanGlobalState(t)
	initConfigForTest(t)

	originalCmdCtx := cmdCtx
	originalJSONOutput := jsonOutput
	originalRootCtx := rootCtx
	defer func() {
		cmdCtx = originalCmdCtx
		jsonOutput = originalJSONOutput
		rootCtx = originalRootCtx
	}()

	resetCommandContext()

	repoDir := t.TempDir()
	beadsDir := filepath.Join(repoDir, ".beads")
	if err := os.MkdirAll(beadsDir, 0o755); err != nil {
		t.Fatalf("mkdir beads dir: %v", err)
	}

	cfg := &configfile.Config{
		Database:     "dolt",
		Backend:      configfile.BackendDolt,
		DoltMode:     configfile.DoltModeEmbedded,
		DoltDatabase: "embedcfg",
	}
	if err := cfg.Save(beadsDir); err != nil {
		t.Fatalf("save metadata: %v", err)
	}

	store, err := embeddeddolt.Open(context.Background(), beadsDir, "embedcfg", "main")
	if err != nil {
		t.Fatalf("embeddeddolt.Open: %v", err)
	}
	if err := store.SetConfig(context.Background(), "issue_prefix", "storeprefix"); err != nil {
		_ = store.Close()
		t.Fatalf("SetConfig(issue_prefix): %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close(): %v", err)
	}

	dbDir := filepath.Join(beadsDir, "dolt")
	t.Setenv("BEADS_DIR", "")
	t.Setenv("BEADS_DB", dbDir)
	t.Setenv("BD_DB", "")
	t.Setenv("BEADS_DOLT_SERVER_MODE", "")
	t.Setenv("BEADS_DOLT_SHARED_SERVER", "")

	dbFlag := rootCmd.PersistentFlags().Lookup("db")
	originalFlagValue := dbFlag.Value.String()
	originalFlagChanged := dbFlag.Changed
	if err := dbFlag.Value.Set(""); err != nil {
		t.Fatalf("reset db flag: %v", err)
	}
	dbFlag.Changed = false
	t.Cleanup(func() {
		_ = dbFlag.Value.Set(originalFlagValue)
		dbFlag.Changed = originalFlagChanged
	})

	jsonOutput = true
	rootCtx = context.Background()

	if err := withStorage(rootCtx, nil, dbDir, func(currentStore storage.DoltStorage) error {
		prefix, err := currentStore.GetConfig(rootCtx, "issue_prefix")
		if err != nil {
			return err
		}
		if prefix != "storeprefix" {
			t.Fatalf("precheck issue_prefix = %q, want %q", prefix, "storeprefix")
		}
		return nil
	}); err != nil {
		t.Fatalf("withStorage precheck: %v", err)
	}

	output := captureStdout(t, func() error {
		return whereCmd.RunE(whereCmd, nil)
	})

	var result WhereResult
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatalf("json.Unmarshal(%q): %v", output, err)
	}

	if !utils.PathsEqual(result.Path, beadsDir) {
		t.Fatalf("Path = %q, want %q", result.Path, beadsDir)
	}
	if result.DatabasePath == "" {
		t.Fatal("DatabasePath = empty, want resolved dolt path")
	}
	base := filepath.Base(result.DatabasePath)
	if base != "dolt" && base != "embeddeddolt" {
		t.Fatalf("DatabasePath = %q, want dolt-style basename", result.DatabasePath)
	}
	if result.Prefix != "storeprefix" {
		t.Fatalf("Prefix = %q, want %q", result.Prefix, "storeprefix")
	}
}

// TestWhereCommand_SurfacesPrefixDrift guards beads-m08v: when config.yaml
// carries a stale issue-prefix that disagrees with the live store's
// issue_prefix, `bd where` must SURFACE the drift (StorePrefix + PrefixStale)
// rather than silently trusting the stale YAML. The primary Prefix field stays
// YAML-first (no precedence flip — preserves the BEADS_DB/BEADS_DIR-workspace
// rationale and the other pinned prefix tests).
func TestWhereCommand_SurfacesPrefixDrift(t *testing.T) {
	saveAndRestoreGlobals(t)
	ensureCleanGlobalState(t)
	initConfigForTest(t)

	originalCmdCtx := cmdCtx
	originalJSONOutput := jsonOutput
	originalRootCtx := rootCtx
	defer func() {
		cmdCtx = originalCmdCtx
		jsonOutput = originalJSONOutput
		rootCtx = originalRootCtx
	}()

	resetCommandContext()

	repoDir := t.TempDir()
	beadsDir := filepath.Join(repoDir, ".beads")
	if err := os.MkdirAll(beadsDir, 0o755); err != nil {
		t.Fatalf("mkdir beads dir: %v", err)
	}

	cfg := &configfile.Config{
		Database:     "dolt",
		Backend:      configfile.BackendDolt,
		DoltMode:     configfile.DoltModeEmbedded,
		DoltDatabase: "driftcfg",
	}
	if err := cfg.Save(beadsDir); err != nil {
		t.Fatalf("save metadata: %v", err)
	}

	// Stale YAML hint that disagrees with the live store below.
	if err := os.WriteFile(filepath.Join(beadsDir, "config.yaml"), []byte("issue-prefix: staleyaml\n"), 0o644); err != nil {
		t.Fatalf("write config.yaml: %v", err)
	}

	store, err := embeddeddolt.Open(context.Background(), beadsDir, "driftcfg", "main")
	if err != nil {
		t.Fatalf("embeddeddolt.Open: %v", err)
	}
	if err := store.SetConfig(context.Background(), "issue_prefix", "livestore"); err != nil {
		_ = store.Close()
		t.Fatalf("SetConfig(issue_prefix): %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close(): %v", err)
	}

	dbDir := filepath.Join(beadsDir, "dolt")
	t.Setenv("BEADS_DIR", "")
	t.Setenv("BEADS_DB", dbDir)
	t.Setenv("BD_DB", "")
	t.Setenv("BEADS_DOLT_SERVER_MODE", "")
	t.Setenv("BEADS_DOLT_SHARED_SERVER", "")

	dbFlag := rootCmd.PersistentFlags().Lookup("db")
	originalFlagValue := dbFlag.Value.String()
	originalFlagChanged := dbFlag.Changed
	if err := dbFlag.Value.Set(""); err != nil {
		t.Fatalf("reset db flag: %v", err)
	}
	dbFlag.Changed = false
	t.Cleanup(func() {
		_ = dbFlag.Value.Set(originalFlagValue)
		dbFlag.Changed = originalFlagChanged
	})

	jsonOutput = true
	rootCtx = context.Background()

	output := captureStdout(t, func() error {
		return whereCmd.RunE(whereCmd, nil)
	})

	var result WhereResult
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatalf("json.Unmarshal(%q): %v", output, err)
	}

	// Primary Prefix stays YAML-first (no precedence flip).
	if result.Prefix != "staleyaml" {
		t.Fatalf("Prefix = %q, want %q (YAML-first, no precedence flip)", result.Prefix, "staleyaml")
	}
	// Drift must be surfaced.
	if result.StorePrefix != "livestore" {
		t.Fatalf("StorePrefix = %q, want %q", result.StorePrefix, "livestore")
	}
	if !result.PrefixStale {
		t.Fatal("PrefixStale = false, want true when YAML prefix disagrees with store prefix")
	}
}

// TestWhereCommand_NoDriftWhenPrefixesAgree guards that the drift signal is
// NOT raised when YAML and store agree (the common case), so bd where output
// stays clean for healthy workspaces.
func TestWhereCommand_NoDriftWhenPrefixesAgree(t *testing.T) {
	saveAndRestoreGlobals(t)
	ensureCleanGlobalState(t)
	initConfigForTest(t)

	originalCmdCtx := cmdCtx
	originalJSONOutput := jsonOutput
	originalRootCtx := rootCtx
	defer func() {
		cmdCtx = originalCmdCtx
		jsonOutput = originalJSONOutput
		rootCtx = originalRootCtx
	}()

	resetCommandContext()

	repoDir := t.TempDir()
	beadsDir := filepath.Join(repoDir, ".beads")
	if err := os.MkdirAll(beadsDir, 0o755); err != nil {
		t.Fatalf("mkdir beads dir: %v", err)
	}

	cfg := &configfile.Config{
		Database:     "dolt",
		Backend:      configfile.BackendDolt,
		DoltMode:     configfile.DoltModeEmbedded,
		DoltDatabase: "agreecfg",
	}
	if err := cfg.Save(beadsDir); err != nil {
		t.Fatalf("save metadata: %v", err)
	}
	if err := os.WriteFile(filepath.Join(beadsDir, "config.yaml"), []byte("issue-prefix: samepfx\n"), 0o644); err != nil {
		t.Fatalf("write config.yaml: %v", err)
	}

	store, err := embeddeddolt.Open(context.Background(), beadsDir, "agreecfg", "main")
	if err != nil {
		t.Fatalf("embeddeddolt.Open: %v", err)
	}
	if err := store.SetConfig(context.Background(), "issue_prefix", "samepfx"); err != nil {
		_ = store.Close()
		t.Fatalf("SetConfig(issue_prefix): %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close(): %v", err)
	}

	dbDir := filepath.Join(beadsDir, "dolt")
	t.Setenv("BEADS_DIR", "")
	t.Setenv("BEADS_DB", dbDir)
	t.Setenv("BD_DB", "")
	t.Setenv("BEADS_DOLT_SERVER_MODE", "")
	t.Setenv("BEADS_DOLT_SHARED_SERVER", "")

	dbFlag := rootCmd.PersistentFlags().Lookup("db")
	originalFlagValue := dbFlag.Value.String()
	originalFlagChanged := dbFlag.Changed
	if err := dbFlag.Value.Set(""); err != nil {
		t.Fatalf("reset db flag: %v", err)
	}
	dbFlag.Changed = false
	t.Cleanup(func() {
		_ = dbFlag.Value.Set(originalFlagValue)
		dbFlag.Changed = originalFlagChanged
	})

	jsonOutput = true
	rootCtx = context.Background()

	output := captureStdout(t, func() error {
		return whereCmd.RunE(whereCmd, nil)
	})

	var result WhereResult
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatalf("json.Unmarshal(%q): %v", output, err)
	}

	if result.Prefix != "samepfx" {
		t.Fatalf("Prefix = %q, want %q", result.Prefix, "samepfx")
	}
	if result.PrefixStale {
		t.Fatal("PrefixStale = true, want false when YAML and store agree")
	}
}
