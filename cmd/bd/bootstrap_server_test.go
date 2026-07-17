package main

import (
	"path/filepath"
	"testing"

	"github.com/steveyegge/beads/internal/configfile"
)

// beads-rqy8: hermetic tests for the server-config resolution helpers in
// bootstrap.go (verified 0% + no test refs). Focused on the env/config-driven
// branches (isSharedServer=false), which need no live doltserver. Crew shells
// carry BEADS_DOLT_* env; clear the relevant vars for hermeticity.

func clearDoltEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"BEADS_DOLT_SHARED_SERVER", "BEADS_DOLT_DATA_DIR",
		"BEADS_DOLT_SERVER_PORT", "BEADS_DOLT_PORT",
	} {
		t.Setenv(k, "")
	}
}

func TestBootstrapSharedServerMode(t *testing.T) {
	clearDoltEnv(t)

	t.Run("env=1 → true", func(t *testing.T) {
		t.Setenv("BEADS_DOLT_SHARED_SERVER", "1")
		if !bootstrapSharedServerMode(t.TempDir()) {
			t.Error("expected shared-server mode when env=1")
		}
	})

	t.Run("env=true → true", func(t *testing.T) {
		t.Setenv("BEADS_DOLT_SHARED_SERVER", "TRUE")
		if !bootstrapSharedServerMode(t.TempDir()) {
			t.Error("expected shared-server mode when env=TRUE")
		}
	})

	t.Run("unset + no config → false", func(t *testing.T) {
		t.Setenv("BEADS_DOLT_SHARED_SERVER", "")
		if bootstrapSharedServerMode(t.TempDir()) {
			t.Error("expected non-shared when env unset and no config")
		}
	})
}

func TestBootstrapServerDoltDir_NonShared(t *testing.T) {
	clearDoltEnv(t)
	beadsDir := "/ws/.beads"

	t.Run("absolute config data-dir used verbatim", func(t *testing.T) {
		cfg := &configfile.Config{DoltDataDir: "/abs/dolt-data"}
		if got := bootstrapServerDoltDir(beadsDir, cfg, false); got != "/abs/dolt-data" {
			t.Errorf("got %q, want /abs/dolt-data", got)
		}
	})

	t.Run("relative config data-dir joined to beadsDir", func(t *testing.T) {
		cfg := &configfile.Config{DoltDataDir: "sub/dd"}
		want := filepath.Join(beadsDir, "sub/dd")
		if got := bootstrapServerDoltDir(beadsDir, cfg, false); got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("no config data-dir → beadsDir/dolt default", func(t *testing.T) {
		cfg := &configfile.Config{}
		want := filepath.Join(beadsDir, "dolt")
		if got := bootstrapServerDoltDir(beadsDir, cfg, false); got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})
}

func TestBootstrapServerPort_NonShared(t *testing.T) {
	clearDoltEnv(t)

	t.Run("env port wins", func(t *testing.T) {
		t.Setenv("BEADS_DOLT_SERVER_PORT", "9999")
		if got := bootstrapServerPort(t.TempDir(), &configfile.Config{}, false); got != 9999 {
			t.Errorf("got %d, want 9999", got)
		}
	})

	t.Run("invalid env port is ignored (falls through)", func(t *testing.T) {
		t.Setenv("BEADS_DOLT_SERVER_PORT", "notaport")
		got := bootstrapServerPort(t.TempDir(), &configfile.Config{}, false)
		if got == 0 {
			t.Error("expected a resolved default port, not 0, for an invalid env value")
		}
	})
}

func TestServerClonePort(t *testing.T) {
	clearDoltEnv(t)

	t.Run("config DoltServerPort wins", func(t *testing.T) {
		cfg := &configfile.Config{DoltServerPort: 4242}
		if got := serverClonePort(t.TempDir(), cfg); got != 4242 {
			t.Errorf("got %d, want 4242", got)
		}
	})

	t.Run("BEADS_DOLT_SERVER_PORT env used when no config field", func(t *testing.T) {
		t.Setenv("BEADS_DOLT_SERVER_PORT", "5555")
		if got := serverClonePort(t.TempDir(), &configfile.Config{}); got != 5555 {
			t.Errorf("got %d, want 5555", got)
		}
	})

	t.Run("BEADS_DOLT_PORT env fallback", func(t *testing.T) {
		t.Setenv("BEADS_DOLT_PORT", "6666")
		if got := serverClonePort(t.TempDir(), &configfile.Config{}); got != 6666 {
			t.Errorf("got %d, want 6666", got)
		}
	})
}
