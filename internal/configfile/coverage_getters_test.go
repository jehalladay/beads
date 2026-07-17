package configfile

import (
	"path/filepath"
	"regexp"
	"testing"
	"time"
)

// These tests cover pure getters and small helpers that the existing suite
// leaves at 0%. Env-reading getters clear their specific env vars first so an
// ambient Gas Town crew shell can't override the struct field under test (same
// hygiene class as isolateDoltServerEnv / beads-zv8). All hermetic (no DB).

func TestGetStaleClosedIssuesDays(t *testing.T) {
	tests := []struct {
		name string
		set  int
		want int
	}{
		{"negative disables (returns 0)", -1, 0},
		{"zero stays zero", 0, 0},
		{"positive returns value", 30, 30},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Config{StaleClosedIssuesDays: tt.set}
			if got := c.GetStaleClosedIssuesDays(); got != tt.want {
				t.Errorf("GetStaleClosedIssuesDays(%d) = %d, want %d", tt.set, got, tt.want)
			}
		})
	}
}

func TestGetDoltServerSocket(t *testing.T) {
	t.Run("env var wins", func(t *testing.T) {
		t.Setenv("BEADS_DOLT_SERVER_SOCKET", "/run/env.sock")
		c := &Config{DoltServerSocket: "/run/struct.sock"}
		if got := c.GetDoltServerSocket(); got != "/run/env.sock" {
			t.Errorf("GetDoltServerSocket = %q, want /run/env.sock", got)
		}
	})
	t.Run("falls back to struct field", func(t *testing.T) {
		t.Setenv("BEADS_DOLT_SERVER_SOCKET", "")
		c := &Config{DoltServerSocket: "/run/struct.sock"}
		if got := c.GetDoltServerSocket(); got != "/run/struct.sock" {
			t.Errorf("GetDoltServerSocket = %q, want /run/struct.sock", got)
		}
	})
	t.Run("empty means TCP", func(t *testing.T) {
		t.Setenv("BEADS_DOLT_SERVER_SOCKET", "")
		c := &Config{}
		if got := c.GetDoltServerSocket(); got != "" {
			t.Errorf("GetDoltServerSocket = %q, want empty", got)
		}
	})
}

func TestGetGlobalProjectID(t *testing.T) {
	c := &Config{GlobalProjectID: "proj-123"}
	if got := c.GetGlobalProjectID(); got != "proj-123" {
		t.Errorf("GetGlobalProjectID = %q, want proj-123", got)
	}
	if got := (&Config{}).GetGlobalProjectID(); got != "" {
		t.Errorf("GetGlobalProjectID (unset) = %q, want empty", got)
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
		{"env 1 enables", "1", true, false, true},
		{"env true enables", "true", true, false, true},
		{"env TRUE enables (case-insensitive)", "TRUE", true, false, true},
		{"env 0 disables even if field true", "0", true, true, false},
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
				t.Errorf("GetDoltServerTLS = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetDoltRemotesAPIPort(t *testing.T) {
	t.Run("valid env wins", func(t *testing.T) {
		t.Setenv("BEADS_DOLT_REMOTESAPI_PORT", "9090")
		c := &Config{DoltRemotesAPIPort: 7000}
		if got := c.GetDoltRemotesAPIPort(); got != 9090 {
			t.Errorf("GetDoltRemotesAPIPort = %d, want 9090", got)
		}
	})
	t.Run("non-numeric env ignored, falls to struct", func(t *testing.T) {
		t.Setenv("BEADS_DOLT_REMOTESAPI_PORT", "notaport")
		c := &Config{DoltRemotesAPIPort: 7000}
		if got := c.GetDoltRemotesAPIPort(); got != 7000 {
			t.Errorf("GetDoltRemotesAPIPort = %d, want 7000", got)
		}
	})
	t.Run("empty env + unset struct returns default", func(t *testing.T) {
		t.Setenv("BEADS_DOLT_REMOTESAPI_PORT", "")
		c := &Config{}
		if got := c.GetDoltRemotesAPIPort(); got != DefaultDoltRemotesAPIPort {
			t.Errorf("GetDoltRemotesAPIPort = %d, want %d", got, DefaultDoltRemotesAPIPort)
		}
	})
}

func TestGenerateProjectID(t *testing.T) {
	uuidV4 := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	id := GenerateProjectID()
	if !uuidV4.MatchString(id) {
		t.Errorf("GenerateProjectID = %q, does not match UUID v4 format", id)
	}
	// Two calls should almost never collide (fresh random each time).
	if id2 := GenerateProjectID(); id2 == id {
		t.Errorf("GenerateProjectID returned identical IDs twice: %q", id)
	}
}

func TestExternalDoltConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     ExternalDoltConfig
		wantErr bool
	}{
		{"host+port valid", ExternalDoltConfig{Host: "h", Port: 3307}, false},
		{"absolute socket valid", ExternalDoltConfig{Socket: "/run/d.sock"}, false},
		{"socket and host conflict", ExternalDoltConfig{Socket: "/run/d.sock", Host: "h", Port: 1}, true},
		{"nothing set", ExternalDoltConfig{}, true},
		{"host without port", ExternalDoltConfig{Host: "h"}, true},
		{"port without host", ExternalDoltConfig{Port: 3307}, true},
		{"port out of range low", ExternalDoltConfig{Host: "h", Port: -1}, true},
		{"port out of range high", ExternalDoltConfig{Host: "h", Port: 70000}, true},
		{"relative socket rejected", ExternalDoltConfig{Socket: "rel.sock"}, true},
		{"tls cert without key", ExternalDoltConfig{Host: "h", Port: 1, TLSCert: "/c.pem"}, true},
		{"tls key without cert", ExternalDoltConfig{Host: "h", Port: 1, TLSKey: "/k.pem"}, true},
		{"relative tls cert rejected", ExternalDoltConfig{Host: "h", Port: 1, TLSCert: "c.pem", TLSKey: "/k.pem"}, true},
		{"relative tls key rejected", ExternalDoltConfig{Host: "h", Port: 1, TLSCert: "/c.pem", TLSKey: "k.pem"}, true},
		{"absolute tls pair valid", ExternalDoltConfig{Host: "h", Port: 1, TLSCert: "/c.pem", TLSKey: "/k.pem"}, false},
		{"negative keepalive rejected", ExternalDoltConfig{Host: "h", Port: 1, KeepAlivePeriod: -time.Second}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if tt.wantErr && err == nil {
				t.Errorf("Validate() = nil, want error")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("Validate() = %v, want nil", err)
			}
		})
	}
}

func TestProxiedServerClientInfo_ResolvedConfigLogPath(t *testing.T) {
	beadsDir := "/beads/root"

	t.Run("nil receiver returns empty", func(t *testing.T) {
		var i *ProxiedServerClientInfo
		if got := i.ResolvedConfigPath(beadsDir); got != "" {
			t.Errorf("nil ResolvedConfigPath = %q, want empty", got)
		}
		if got := i.ResolvedLogPath(beadsDir); got != "" {
			t.Errorf("nil ResolvedLogPath = %q, want empty", got)
		}
	})

	t.Run("relative paths are joined under beadsDir", func(t *testing.T) {
		i := &ProxiedServerClientInfo{ConfigPath: "cfg.yaml", LogPath: "logs/proxy.log"}
		if got, want := i.ResolvedConfigPath(beadsDir), filepath.Join(beadsDir, "cfg.yaml"); got != want {
			t.Errorf("ResolvedConfigPath = %q, want %q", got, want)
		}
		if got, want := i.ResolvedLogPath(beadsDir), filepath.Join(beadsDir, "logs/proxy.log"); got != want {
			t.Errorf("ResolvedLogPath = %q, want %q", got, want)
		}
	})

	t.Run("absolute paths pass through unchanged", func(t *testing.T) {
		i := &ProxiedServerClientInfo{ConfigPath: "/abs/cfg.yaml", LogPath: "/abs/proxy.log"}
		if got := i.ResolvedConfigPath(beadsDir); got != "/abs/cfg.yaml" {
			t.Errorf("ResolvedConfigPath = %q, want /abs/cfg.yaml", got)
		}
		if got := i.ResolvedLogPath(beadsDir); got != "/abs/proxy.log" {
			t.Errorf("ResolvedLogPath = %q, want /abs/proxy.log", got)
		}
	})

	t.Run("empty path stays empty", func(t *testing.T) {
		i := &ProxiedServerClientInfo{}
		if got := i.ResolvedConfigPath(beadsDir); got != "" {
			t.Errorf("ResolvedConfigPath (empty) = %q, want empty", got)
		}
		if got := i.ResolvedLogPath(beadsDir); got != "" {
			t.Errorf("ResolvedLogPath (empty) = %q, want empty", got)
		}
	})
}
