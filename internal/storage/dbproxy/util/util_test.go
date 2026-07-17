package util

import (
	"strings"
	"testing"
	"time"

	mysql "github.com/go-sql-driver/mysql"

	"github.com/steveyegge/beads/internal/lockfile"
)

// parseDSN round-trips a DSN string back through the driver's parser so tests
// can assert on the decoded config rather than brittle string formatting.
func parseDSN(t *testing.T, dsn string) *mysql.Config {
	t.Helper()
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("DoltServerDSN produced an unparseable DSN %q: %v", dsn, err)
	}
	return cfg
}

func TestDoltServerDSN_TCP(t *testing.T) {
	d := DoltServerDSN{
		Host:     "127.0.0.1",
		Port:     3307,
		User:     "root",
		Password: "secret",
		Database: "hq",
	}
	cfg := parseDSN(t, d.String())

	if cfg.Net != "tcp" {
		t.Errorf("Net = %q, want tcp", cfg.Net)
	}
	if cfg.Addr != "127.0.0.1:3307" {
		t.Errorf("Addr = %q, want 127.0.0.1:3307", cfg.Addr)
	}
	if cfg.User != "root" {
		t.Errorf("User = %q, want root", cfg.User)
	}
	if cfg.Passwd != "secret" {
		t.Errorf("Passwd = %q, want secret", cfg.Passwd)
	}
	if cfg.DBName != "hq" {
		t.Errorf("DBName = %q, want hq", cfg.DBName)
	}
	if !cfg.ParseTime {
		t.Error("ParseTime = false, want true")
	}
	if !cfg.MultiStatements {
		t.Error("MultiStatements = false, want true")
	}
	if !cfg.AllowNativePasswords {
		t.Error("AllowNativePasswords = false, want true")
	}
}

func TestDoltServerDSN_Socket(t *testing.T) {
	d := DoltServerDSN{
		Socket:   "/tmp/mysql.sock",
		Host:     "ignored-when-socket-set",
		Port:     9999,
		User:     "root",
		Database: "hq",
	}
	cfg := parseDSN(t, d.String())

	if cfg.Net != "unix" {
		t.Errorf("Net = %q, want unix (socket set)", cfg.Net)
	}
	if cfg.Addr != "/tmp/mysql.sock" {
		t.Errorf("Addr = %q, want the socket path", cfg.Addr)
	}
}

func TestDoltServerDSN_DefaultTimeout(t *testing.T) {
	// Timeout unset (zero) must default to 5s, not 0 (which the driver treats
	// as "no timeout" — a hang risk this code deliberately guards against).
	cfg := parseDSN(t, DoltServerDSN{Host: "h", Port: 1}.String())
	if cfg.Timeout != 5*time.Second {
		t.Errorf("default Timeout = %v, want 5s", cfg.Timeout)
	}
}

func TestDoltServerDSN_ExplicitTimeout(t *testing.T) {
	cfg := parseDSN(t, DoltServerDSN{Host: "h", Port: 1, Timeout: 12 * time.Second}.String())
	if cfg.Timeout != 12*time.Second {
		t.Errorf("Timeout = %v, want 12s", cfg.Timeout)
	}
}

func TestDoltServerDSN_TLS(t *testing.T) {
	tests := []struct {
		name     string
		required bool
		want     string
	}{
		{"tls required", true, "true"},
		{"tls not required", false, "false"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// The driver only exposes named TLS configs on the parsed struct
			// via TLSConfig; assert on the raw DSN param instead.
			dsn := DoltServerDSN{Host: "h", Port: 1, TLSRequired: tt.required}.String()
			if !strings.Contains(dsn, "tls="+tt.want) {
				t.Errorf("DSN %q missing tls=%s", dsn, tt.want)
			}
		})
	}
}

func TestTryLock_AcquireAndRelease(t *testing.T) {
	// Lock path in a nested dir that does not yet exist — TryLock must create
	// the parent directory.
	lockPath := t.TempDir() + "/nested/dir/beads.lock"

	l, err := TryLock(lockPath)
	if err != nil {
		t.Fatalf("TryLock on a fresh path failed: %v", err)
	}
	if l.File() == nil {
		t.Fatal("Lock.File() returned nil after a successful lock")
	}
	// Releasing must not panic.
	l.Unlock()
}

func TestTryLock_Contention(t *testing.T) {
	lockPath := t.TempDir() + "/beads.lock"

	first, err := TryLock(lockPath)
	if err != nil {
		t.Fatalf("first TryLock failed: %v", err)
	}
	defer first.Unlock()

	// A second non-blocking attempt on a held lock must fail with an error
	// that reports "already locked" so callers can distinguish contention.
	second, err := TryLock(lockPath)
	if err == nil {
		second.Unlock()
		t.Fatal("second TryLock succeeded while the lock was held; want contention error")
	}
	if !lockfile.IsLocked(err) {
		t.Errorf("contention error %v does not satisfy lockfile.IsLocked", err)
	}
}

func TestTryLock_ReacquireAfterUnlock(t *testing.T) {
	lockPath := t.TempDir() + "/beads.lock"

	l1, err := TryLock(lockPath)
	if err != nil {
		t.Fatalf("first TryLock failed: %v", err)
	}
	l1.Unlock()

	// After release the same path must be lockable again.
	l2, err := TryLock(lockPath)
	if err != nil {
		t.Fatalf("re-acquire after unlock failed: %v", err)
	}
	l2.Unlock()
}

func TestNoopLock_Unlock(t *testing.T) {
	// NoopLock.Unlock must be safe to call and satisfy the Unlocker interface.
	var u Unlocker = NoopLock{}
	u.Unlock() // must not panic
}
