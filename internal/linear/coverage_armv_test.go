package linear

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// When beadsDir cannot be created (a regular file sits where the directory
// should be), AcquireSyncLock fails at the MkdirAll guard.
func TestAcquireSyncLock_MkdirAllError(t *testing.T) {
	base := t.TempDir()
	// Create a file, then try to use a path *under* it as beadsDir — MkdirAll
	// must fail because a parent component is not a directory.
	blocker := filepath.Join(base, "notadir")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatalf("write blocker: %v", err)
	}
	_, err := AcquireSyncLock(filepath.Join(blocker, "sub"), false)
	if err == nil {
		t.Fatal("expected MkdirAll error when a parent path component is a file")
	}
}

// When the lock path exists but is a directory, OpenFile(O_RDWR) fails and
// AcquireSyncLock surfaces the "opening lock file" error.
func TestAcquireSyncLock_OpenFileError(t *testing.T) {
	dir := t.TempDir()
	// Pre-create the lock *path* as a directory so O_RDWR open fails.
	if err := os.Mkdir(filepath.Join(dir, syncLockFilename), 0o755); err != nil {
		t.Fatalf("mkdir lock-as-dir: %v", err)
	}
	_, err := AcquireSyncLock(dir, false)
	if err == nil {
		t.Fatal("expected OpenFile error when lock path is a directory")
	}
}

// The blocking (wait=true) path acquires immediately when the lock is free,
// exercising FlockExclusiveBlocking.
func TestAcquireSyncLock_BlockingAcquiresWhenFree(t *testing.T) {
	dir := t.TempDir()
	lock, err := AcquireSyncLock(dir, true)
	if err != nil {
		t.Fatalf("blocking acquire on free lock: %v", err)
	}
	if lock == nil {
		t.Fatal("expected non-nil lock")
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("release: %v", err)
	}
}

// A second blocking acquire must wait until the first releases, then succeed —
// exercises the blocking branch under real contention within one process.
func TestAcquireSyncLock_BlockingWaitsForRelease(t *testing.T) {
	dir := t.TempDir()
	lock1, err := AcquireSyncLock(dir, false)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}

	acquired := make(chan struct{})
	var lock2 *SyncLock
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		l, err := AcquireSyncLock(dir, true) // blocks until lock1 releases
		if err != nil {
			t.Errorf("blocking acquire: %v", err)
			return
		}
		lock2 = l
		close(acquired)
	}()

	// Give the goroutine a moment to block on the flock, then release.
	time.Sleep(50 * time.Millisecond)
	select {
	case <-acquired:
		t.Fatal("blocking acquire returned before the holder released")
	default:
	}

	if err := lock1.Release(); err != nil {
		t.Fatalf("release lock1: %v", err)
	}

	select {
	case <-acquired:
	case <-time.After(2 * time.Second):
		t.Fatal("blocking acquire did not complete after release")
	}
	wg.Wait()
	if lock2 != nil {
		_ = lock2.Release()
	}
}

// readLockInfo on a missing file returns nil (the ReadFile error branch).
func TestReadLockInfo_MissingFile(t *testing.T) {
	if got := readLockInfo(filepath.Join(t.TempDir(), "does-not-exist.lock")); got != nil {
		t.Errorf("expected nil for missing lock file, got %+v", got)
	}
}

// readLockInfo on a well-formed file returns the parsed PID/started.
func TestReadLockInfo_ValidFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "info.lock")
	if err := os.WriteFile(p, []byte("pid=4321\nstarted=2026-03-04T05:06:07Z\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	info := readLockInfo(p)
	if info == nil {
		t.Fatal("expected non-nil info")
	}
	if info.PID != 4321 {
		t.Errorf("PID = %d, want 4321", info.PID)
	}
	if info.Started.IsZero() {
		t.Error("expected non-zero started time")
	}
}

// writeLockInfo writes a parseable pid/started record that round-trips back
// through readLockInfo.
func TestWriteLockInfo_RoundTrips(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "rt.lock")
	f, err := os.OpenFile(p, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()

	if err := writeLockInfo(f); err != nil {
		t.Fatalf("writeLockInfo: %v", err)
	}
	info := readLockInfo(p)
	if info == nil {
		t.Fatal("expected round-tripped info")
	}
	if info.PID != os.Getpid() {
		t.Errorf("PID = %d, want current %d", info.PID, os.Getpid())
	}
}
