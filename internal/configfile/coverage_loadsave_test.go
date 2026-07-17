package configfile

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestLoad_AbsentReturnsNil covers the Load early-out where neither
// metadata.json nor the legacy config.json exists: it must return (nil, nil).
func TestLoad_AbsentReturnsNil(t *testing.T) {
	dir := t.TempDir()
	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load on empty dir: %v", err)
	}
	if cfg != nil {
		t.Errorf("Load on empty dir = %+v, want nil", cfg)
	}
}

// TestLoad_MetadataRoundTrip covers the primary read path: a metadata.json
// written by Save is parsed back into an equivalent Config.
func TestLoad_MetadataRoundTrip(t *testing.T) {
	dir := t.TempDir()
	want := &Config{Database: "beads.db", Backend: BackendDolt, StaleClosedIssuesDays: 9}
	if err := want.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load(dir)
	if err != nil || got == nil {
		t.Fatalf("Load: err=%v got=%v", err, got)
	}
	if got.Database != want.Database || got.Backend != want.Backend || got.StaleClosedIssuesDays != want.StaleClosedIssuesDays {
		t.Errorf("Load round-trip = %+v, want %+v", got, want)
	}
}

// TestLoad_InvalidJSON covers the parse-error branch of Load for the primary
// metadata.json path.
func TestLoad_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(ConfigPath(dir), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("seed bad metadata: %v", err)
	}
	if _, err := Load(dir); err == nil {
		t.Error("Load with invalid metadata.json returned nil error, want parse error")
	}
}

// TestLoad_LegacyMigration covers the migration branch: metadata.json is
// absent but a legacy config.json exists. Load must parse it, write a new
// metadata.json, remove the legacy file, and return the parsed Config.
func TestLoad_LegacyMigration(t *testing.T) {
	dir := t.TempDir()
	legacyPath := filepath.Join(dir, "config.json")
	legacy := &Config{Database: "beads.db", Backend: BackendDolt, StaleClosedIssuesDays: 7}
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatalf("marshal legacy: %v", err)
	}
	if err := os.WriteFile(legacyPath, data, 0o600); err != nil {
		t.Fatalf("seed legacy config.json: %v", err)
	}

	got, err := Load(dir)
	if err != nil || got == nil {
		t.Fatalf("Load(legacy): err=%v got=%v", err, got)
	}
	if got.StaleClosedIssuesDays != 7 {
		t.Errorf("migrated StaleClosedIssuesDays = %d, want 7", got.StaleClosedIssuesDays)
	}
	// Migration must create metadata.json at the new location.
	if _, err := os.Stat(ConfigPath(dir)); err != nil {
		t.Errorf("migration did not create %s: %v", ConfigFileName, err)
	}
	// Migration must remove the legacy config.json (best effort).
	if _, err := os.Stat(legacyPath); !os.IsNotExist(err) {
		t.Errorf("legacy config.json still present after migration (err=%v)", err)
	}
}

// TestLoad_LegacyInvalidJSON covers the parse-error branch of the legacy
// migration path.
func TestLoad_LegacyInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("{bad"), 0o600); err != nil {
		t.Fatalf("seed bad legacy: %v", err)
	}
	if _, err := Load(dir); err == nil {
		t.Error("Load with invalid legacy config.json returned nil error, want parse error")
	}
}

// TestSave_StripsAbsoluteDoltDataDir covers the Save branch that blanks an
// absolute DoltDataDir before persisting (absolute paths are machine-specific
// and must not be written into the shared metadata.json).
func TestSave_StripsAbsoluteDoltDataDir(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{Database: "beads.db", Backend: BackendDolt, DoltDataDir: "/abs/machine/specific/dolt"}
	if err := cfg.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}
	// The in-memory config must be untouched by Save.
	if cfg.DoltDataDir != "/abs/machine/specific/dolt" {
		t.Errorf("Save mutated caller's DoltDataDir = %q", cfg.DoltDataDir)
	}
	// The persisted file must NOT contain the absolute path.
	raw, err := os.ReadFile(ConfigPath(dir))
	if err != nil {
		t.Fatalf("read saved: %v", err)
	}
	reloaded, err := Load(dir)
	if err != nil || reloaded == nil {
		t.Fatalf("Load: err=%v got=%v", err, reloaded)
	}
	if reloaded.DoltDataDir != "" {
		t.Errorf("persisted DoltDataDir = %q, want empty (absolute stripped); raw=%s", reloaded.DoltDataDir, raw)
	}
}

// TestSave_KeepsRelativeDoltDataDir confirms a relative DoltDataDir is
// preserved through Save/Load (only absolute paths are stripped).
func TestSave_KeepsRelativeDoltDataDir(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{Database: "beads.db", Backend: BackendDolt, DoltDataDir: "fastdisk/dolt"}
	if err := cfg.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}
	reloaded, err := Load(dir)
	if err != nil || reloaded == nil {
		t.Fatalf("Load: err=%v got=%v", err, reloaded)
	}
	if reloaded.DoltDataDir != "fastdisk/dolt" {
		t.Errorf("persisted relative DoltDataDir = %q, want fastdisk/dolt", reloaded.DoltDataDir)
	}
}

// TestDatabasePath_CustomDoltDataDir covers the two custom-data-dir branches of
// DatabasePath: an absolute custom dir is returned verbatim, a relative one is
// joined onto beadsDir.
func TestDatabasePath_CustomDoltDataDir(t *testing.T) {
	beadsDir := filepath.Join("home", "user", "project", ".beads")

	t.Run("absolute custom dir returned verbatim", func(t *testing.T) {
		cfg := &Config{DoltDataDir: string(filepath.Separator) + filepath.Join("mnt", "fast", "dolt")}
		got := cfg.DatabasePath(beadsDir)
		if got != cfg.DoltDataDir {
			t.Errorf("DatabasePath() = %q, want absolute %q", got, cfg.DoltDataDir)
		}
	})

	t.Run("relative custom dir joined to beadsDir", func(t *testing.T) {
		cfg := &Config{DoltDataDir: "fastdisk/dolt"}
		got := cfg.DatabasePath(beadsDir)
		want := filepath.Join(beadsDir, "fastdisk/dolt")
		if got != want {
			t.Errorf("DatabasePath() = %q, want %q", got, want)
		}
	})

	t.Run("absolute Database field returned verbatim when no custom dir", func(t *testing.T) {
		abs := string(filepath.Separator) + filepath.Join("srv", "dolt-data")
		cfg := &Config{Database: abs}
		got := cfg.DatabasePath(beadsDir)
		if got != abs {
			t.Errorf("DatabasePath() = %q, want absolute Database %q", got, abs)
		}
	})
}

// TestLoadProxiedServerClientInfo_InvalidJSON covers the parse-error branch of
// LoadProxiedServerClientInfo (the round-trip test only exercises the happy
// paths and the absent-file early-out).
func TestLoadProxiedServerClientInfo_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(ProxiedServerClientInfoPath(dir), []byte("{broken"), 0o600); err != nil {
		t.Fatalf("seed bad sidecar: %v", err)
	}
	if _, err := LoadProxiedServerClientInfo(dir); err == nil {
		t.Error("LoadProxiedServerClientInfo with invalid JSON returned nil error, want parse error")
	}
}

// TestSaveProxiedServerClientInfo_NilWritesEmpty covers the nil-guard branch of
// SaveProxiedServerClientInfo: a nil info is persisted as an empty object that
// loads back as a non-nil zero-value struct.
func TestSaveProxiedServerClientInfo_NilWritesEmpty(t *testing.T) {
	dir := t.TempDir()
	if err := SaveProxiedServerClientInfo(dir, nil); err != nil {
		t.Fatalf("Save(nil): %v", err)
	}
	got, err := LoadProxiedServerClientInfo(dir)
	if err != nil || got == nil {
		t.Fatalf("Load after Save(nil): err=%v got=%v", err, got)
	}
	if got.RootPath != "" || got.ConfigPath != "" || got.LogPath != "" || got.External != nil {
		t.Errorf("Save(nil) produced non-zero sidecar: %+v", got)
	}
}

// TestResolvedPaths_NilReceiver covers the nil-receiver guards on the resolver
// methods (Resolved*Path on a nil *ProxiedServerClientInfo returns "").
func TestResolvedPaths_NilReceiver(t *testing.T) {
	var info *ProxiedServerClientInfo
	if got := info.ResolvedRootPath("/beads"); got != "" {
		t.Errorf("ResolvedRootPath(nil) = %q, want empty", got)
	}
	if got := info.ResolvedConfigPath("/beads"); got != "" {
		t.Errorf("ResolvedConfigPath(nil) = %q, want empty", got)
	}
	if got := info.ResolvedLogPath("/beads"); got != "" {
		t.Errorf("ResolvedLogPath(nil) = %q, want empty", got)
	}
}

// TestResolvedPaths_RelativeJoinedAbsoluteVerbatim covers resolveSidecarPath's
// three branches (empty → "", absolute → verbatim, relative → joined) via the
// exported resolver methods.
func TestResolvedPaths_RelativeJoinedAbsoluteVerbatim(t *testing.T) {
	beadsDir := string(filepath.Separator) + "beads"
	abs := string(filepath.Separator) + filepath.Join("etc", "dolt", "server.yaml")
	info := &ProxiedServerClientInfo{
		RootPath:   "relroot",
		ConfigPath: abs,
		LogPath:    "",
	}
	if got, want := info.ResolvedRootPath(beadsDir), filepath.Join(beadsDir, "relroot"); got != want {
		t.Errorf("ResolvedRootPath = %q, want %q", got, want)
	}
	if got := info.ResolvedConfigPath(beadsDir); got != abs {
		t.Errorf("ResolvedConfigPath = %q, want absolute %q", got, abs)
	}
	if got := info.ResolvedLogPath(beadsDir); got != "" {
		t.Errorf("ResolvedLogPath(empty) = %q, want empty", got)
	}
}
