package configfile

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestGetStaleClosedIssuesDays(t *testing.T) {
	tests := []struct {
		name string
		set  int
		want int
	}{
		{"negative disables (returns 0)", -5, 0},
		{"zero is zero", 0, 0},
		{"positive passes through", 30, 30},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Config{StaleClosedIssuesDays: tt.set}
			if got := c.GetStaleClosedIssuesDays(); got != tt.want {
				t.Errorf("GetStaleClosedIssuesDays() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestGetDoltServerSocket(t *testing.T) {
	t.Run("env var wins", func(t *testing.T) {
		t.Setenv("BEADS_DOLT_SERVER_SOCKET", "/tmp/env.sock")
		c := &Config{DoltServerSocket: "/tmp/config.sock"}
		if got := c.GetDoltServerSocket(); got != "/tmp/env.sock" {
			t.Errorf("got %q, want /tmp/env.sock", got)
		}
	})
	t.Run("falls back to config", func(t *testing.T) {
		t.Setenv("BEADS_DOLT_SERVER_SOCKET", "")
		c := &Config{DoltServerSocket: "/tmp/config.sock"}
		if got := c.GetDoltServerSocket(); got != "/tmp/config.sock" {
			t.Errorf("got %q, want /tmp/config.sock", got)
		}
	})
}

func TestGetGlobalProjectID(t *testing.T) {
	c := &Config{GlobalProjectID: "proj-123"}
	if got := c.GetGlobalProjectID(); got != "proj-123" {
		t.Errorf("got %q, want proj-123", got)
	}
	if got := (&Config{}).GetGlobalProjectID(); got != "" {
		t.Errorf("got %q, want empty for unset", got)
	}
}

func TestGetDoltServerTLS(t *testing.T) {
	tests := []struct {
		name   string
		env    string
		setEnv bool
		field  bool
		want   bool
	}{
		{"env 1 -> true", "1", true, false, true},
		{"env true -> true", "true", true, false, true},
		{"env TRUE (case-insensitive) -> true", "TRUE", true, false, true},
		{"env 0 -> false", "0", true, true, false},
		{"env other -> false", "no", true, true, false},
		{"no env falls back to field true", "", false, true, true},
		{"no env falls back to field false", "", false, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setEnv {
				t.Setenv("BEADS_DOLT_SERVER_TLS", tt.env)
			} else {
				t.Setenv("BEADS_DOLT_SERVER_TLS", "")
			}
			c := &Config{DoltServerTLS: tt.field}
			if got := c.GetDoltServerTLS(); got != tt.want {
				t.Errorf("GetDoltServerTLS() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetDoltRemotesAPIPort(t *testing.T) {
	t.Run("valid env wins", func(t *testing.T) {
		t.Setenv("BEADS_DOLT_REMOTESAPI_PORT", "9091")
		c := &Config{DoltRemotesAPIPort: 8081}
		if got := c.GetDoltRemotesAPIPort(); got != 9091 {
			t.Errorf("got %d, want 9091", got)
		}
	})
	t.Run("non-numeric env falls through to config", func(t *testing.T) {
		t.Setenv("BEADS_DOLT_REMOTESAPI_PORT", "not-a-number")
		c := &Config{DoltRemotesAPIPort: 8081}
		if got := c.GetDoltRemotesAPIPort(); got != 8081 {
			t.Errorf("got %d, want 8081 (config value)", got)
		}
	})
	t.Run("no env, positive config", func(t *testing.T) {
		t.Setenv("BEADS_DOLT_REMOTESAPI_PORT", "")
		c := &Config{DoltRemotesAPIPort: 8081}
		if got := c.GetDoltRemotesAPIPort(); got != 8081 {
			t.Errorf("got %d, want 8081", got)
		}
	})
	t.Run("no env, unset config -> default", func(t *testing.T) {
		t.Setenv("BEADS_DOLT_REMOTESAPI_PORT", "")
		c := &Config{}
		if got := c.GetDoltRemotesAPIPort(); got != DefaultDoltRemotesAPIPort {
			t.Errorf("got %d, want default %d", got, DefaultDoltRemotesAPIPort)
		}
	})
}

// GenerateProjectID must produce a syntactically valid RFC-4122 v4 UUID with
// the version nibble (4) and variant bits set, and distinct values per call.
func TestGenerateProjectID(t *testing.T) {
	re := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		id := GenerateProjectID()
		if !re.MatchString(id) {
			t.Fatalf("GenerateProjectID() = %q, not a valid v4 UUID", id)
		}
		if seen[id] {
			t.Fatalf("GenerateProjectID() produced a duplicate: %q", id)
		}
		seen[id] = true
	}
}

func TestExternalDoltConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     ExternalDoltConfig
		wantErr string // substring; "" means expect nil
	}{
		{"host+port valid", ExternalDoltConfig{Host: "h", Port: 3307}, ""},
		{"absolute socket valid", ExternalDoltConfig{Socket: "/tmp/d.sock"}, ""},
		{"socket AND host/port rejected", ExternalDoltConfig{Socket: "/tmp/d.sock", Host: "h", Port: 1}, "not both"},
		{"nothing set rejected", ExternalDoltConfig{}, "must set Socket"},
		{"host without port rejected", ExternalDoltConfig{Host: "h"}, "Host requires Port"},
		{"port without host rejected", ExternalDoltConfig{Port: 3307}, "Port requires Host"},
		{"port out of range rejected", ExternalDoltConfig{Host: "h", Port: 70000}, "out of range"},
		{"relative socket rejected", ExternalDoltConfig{Socket: "rel.sock"}, "not absolute"},
		{"cert without key rejected", ExternalDoltConfig{Host: "h", Port: 1, TLSCert: "/c"}, "TLSCert set without TLSKey"},
		{"key without cert rejected", ExternalDoltConfig{Host: "h", Port: 1, TLSKey: "/k"}, "TLSKey set without TLSCert"},
		{"relative cert rejected", ExternalDoltConfig{Host: "h", Port: 1, TLSCert: "c", TLSKey: "/k"}, "TLSCert"},
		{"relative key rejected", ExternalDoltConfig{Host: "h", Port: 1, TLSCert: "/c", TLSKey: "k"}, "TLSKey"},
		{"absolute cert+key valid", ExternalDoltConfig{Host: "h", Port: 1, TLSCert: "/c", TLSKey: "/k"}, ""},
		{"negative keepalive rejected", ExternalDoltConfig{Host: "h", Port: 1, KeepAlivePeriod: -time.Second}, "negative"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Errorf("Validate() = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Validate() = nil, want error containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("Validate() = %q, want substring %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestProxiedServerClientInfo_ResolvedConfigAndLogPaths(t *testing.T) {
	beadsDir := "/base/.beads"

	t.Run("nil receiver returns empty", func(t *testing.T) {
		var i *ProxiedServerClientInfo
		if got := i.ResolvedConfigPath(beadsDir); got != "" {
			t.Errorf("ResolvedConfigPath on nil = %q, want empty", got)
		}
		if got := i.ResolvedLogPath(beadsDir); got != "" {
			t.Errorf("ResolvedLogPath on nil = %q, want empty", got)
		}
		if got := i.ResolvedRootPath(beadsDir); got != "" {
			t.Errorf("ResolvedRootPath on nil = %q, want empty", got)
		}
	})

	t.Run("relative path joined to beadsDir", func(t *testing.T) {
		i := &ProxiedServerClientInfo{ConfigPath: "cfg.json", LogPath: "log.txt"}
		if got := i.ResolvedConfigPath(beadsDir); got != filepath.Join(beadsDir, "cfg.json") {
			t.Errorf("ResolvedConfigPath = %q", got)
		}
		if got := i.ResolvedLogPath(beadsDir); got != filepath.Join(beadsDir, "log.txt") {
			t.Errorf("ResolvedLogPath = %q", got)
		}
	})

	t.Run("absolute path unchanged", func(t *testing.T) {
		i := &ProxiedServerClientInfo{ConfigPath: "/abs/cfg.json"}
		if got := i.ResolvedConfigPath(beadsDir); got != "/abs/cfg.json" {
			t.Errorf("ResolvedConfigPath = %q, want /abs/cfg.json", got)
		}
	})

	t.Run("empty path stays empty", func(t *testing.T) {
		i := &ProxiedServerClientInfo{}
		if got := i.ResolvedLogPath(beadsDir); got != "" {
			t.Errorf("ResolvedLogPath = %q, want empty", got)
		}
	})
}

// SaveProxiedServerClientInfo then LoadProxiedServerClientInfo must round-trip;
// a missing file loads as (nil, nil).
func TestProxiedServerClientInfo_SaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()

	// Missing file: nil info, nil error.
	got, err := LoadProxiedServerClientInfo(dir)
	if err != nil {
		t.Fatalf("Load on missing file: %v", err)
	}
	if got != nil {
		t.Fatalf("Load on missing file = %#v, want nil", got)
	}

	// nil info saves as an empty struct.
	if err := SaveProxiedServerClientInfo(dir, nil); err != nil {
		t.Fatalf("Save(nil): %v", err)
	}
	loaded, err := LoadProxiedServerClientInfo(dir)
	if err != nil {
		t.Fatalf("Load after Save(nil): %v", err)
	}
	if loaded == nil {
		t.Fatal("expected a non-nil info after saving an empty struct")
	}

	// Round-trip a populated struct.
	want := &ProxiedServerClientInfo{
		RootPath:   "root",
		ConfigPath: "cfg.json",
		LogPath:    "log.txt",
		External:   &ExternalDoltConfig{Host: "h", Port: 3307},
	}
	if err := SaveProxiedServerClientInfo(dir, want); err != nil {
		t.Fatalf("Save(populated): %v", err)
	}
	loaded, err = LoadProxiedServerClientInfo(dir)
	if err != nil {
		t.Fatalf("Load(populated): %v", err)
	}
	if loaded.RootPath != want.RootPath || loaded.ConfigPath != want.ConfigPath || loaded.LogPath != want.LogPath {
		t.Errorf("round-trip paths mismatch: got %#v", loaded)
	}
	if loaded.External == nil || loaded.External.Host != "h" || loaded.External.Port != 3307 {
		t.Errorf("round-trip external mismatch: got %#v", loaded.External)
	}
}

func TestLoadProxiedServerClientInfo_BadJSON(t *testing.T) {
	dir := t.TempDir()
	path := ProxiedServerClientInfoPath(dir)
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadProxiedServerClientInfo(dir); err == nil {
		t.Fatal("expected a parse error for malformed JSON")
	}
}
