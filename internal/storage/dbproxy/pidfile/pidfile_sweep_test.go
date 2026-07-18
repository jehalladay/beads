package pidfile

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestWriteSweepsOrphanedTemp asserts that pidfile.Write opportunistically
// removes a crash-orphaned atomicfile temp (".~<...>") left in rootDir by a
// prior proxy that was SIGKILLed between Create and rename (beads-9o6s). Only
// stale orphans are swept; a fresh temp (a concurrent in-flight write) MUST be
// left untouched — same safety property as beads-qoda's SweepStale age gate.
func TestWriteSweepsOrphanedTemp(t *testing.T) {
	dir := t.TempDir()

	// A crash-orphaned temp: hidden ".~" prefix, mtime well in the past.
	orphan := filepath.Join(dir, ".~proxy.pid.123456")
	if err := os.WriteFile(orphan, []byte("stale"), 0o600); err != nil {
		t.Fatalf("seed orphan: %v", err)
	}
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(orphan, old, old); err != nil {
		t.Fatalf("age orphan: %v", err)
	}

	// A fresh temp mimicking a concurrent live write: recent mtime.
	fresh := filepath.Join(dir, ".~proxy.pid.999999")
	if err := os.WriteFile(fresh, []byte("live"), 0o600); err != nil {
		t.Fatalf("seed fresh: %v", err)
	}

	if err := Write(dir, "proxy.pid", PidFile{Pid: 1, Port: 2}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Errorf("stale orphan temp should have been swept, but it survives (err=%v)", err)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Errorf("fresh in-flight temp must NOT be swept, but it is gone: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "proxy.pid")); err != nil {
		t.Errorf("pidfile should have been written: %v", err)
	}
}
