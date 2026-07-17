package remotecache

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/storage"
)

// These tests exercise the filesystem-only cache paths (OpenStore, acquireLock,
// writeMeta/readMeta, Evict) with no dolt CLI: OpenStore returns whatever the
// StoreOpener returns, and doltExists only stats a .dolt directory we fabricate.

const testRemoteURL = "https://example.com/owner/repo"

// mkDoltDir fabricates a <cloneTarget>/.dolt directory so doltExists reports true.
func mkDoltDir(t *testing.T, c *Cache, remoteURL string) {
	t.Helper()
	doltDir := filepath.Join(c.cloneTarget(remoteURL), ".dolt")
	if err := os.MkdirAll(doltDir, 0o755); err != nil {
		t.Fatalf("mkdir .dolt: %v", err)
	}
}

func TestOpenStore_NoCachedClone(t *testing.T) {
	c := &Cache{Dir: t.TempDir()}
	called := false
	opener := func(ctx context.Context, beadsDir string) (storage.DoltStorage, error) {
		called = true
		return nil, nil
	}
	_, err := c.OpenStore(context.Background(), testRemoteURL, opener)
	if err == nil {
		t.Fatal("expected error when no cached clone exists")
	}
	if called {
		t.Error("opener should not be called when clone is absent")
	}
}

func TestOpenStore_DelegatesToOpener(t *testing.T) {
	c := &Cache{Dir: t.TempDir()}
	mkDoltDir(t, c, testRemoteURL)

	var gotDir string
	opener := func(ctx context.Context, beadsDir string) (storage.DoltStorage, error) {
		gotDir = beadsDir
		return nil, nil // nil store is fine — OpenStore returns the opener's result verbatim
	}
	if _, err := c.OpenStore(context.Background(), testRemoteURL, opener); err != nil {
		t.Fatalf("OpenStore unexpected error: %v", err)
	}
	if want := c.entryDir(testRemoteURL); gotDir != want {
		t.Errorf("opener beadsDir = %q, want entry dir %q", gotDir, want)
	}
}

func TestWriteReadMeta_Roundtrip(t *testing.T) {
	c := &Cache{Dir: t.TempDir()}
	// The entry dir must exist before writeMeta can write into it.
	if err := os.MkdirAll(c.entryDir(testRemoteURL), 0o755); err != nil {
		t.Fatalf("mkdir entry: %v", err)
	}

	orig := &CacheMeta{RemoteURL: testRemoteURL, LastPull: 111, LastPush: 222}
	c.writeMeta(testRemoteURL, orig)

	got := c.readMeta(testRemoteURL)
	if got.RemoteURL != orig.RemoteURL || got.LastPull != orig.LastPull || got.LastPush != orig.LastPush {
		t.Errorf("readMeta = %+v, want %+v", got, orig)
	}
}

func TestReadMeta_MissingFileReturnsDefault(t *testing.T) {
	c := &Cache{Dir: t.TempDir()}
	got := c.readMeta(testRemoteURL)
	if got.RemoteURL != testRemoteURL {
		t.Errorf("readMeta(missing).RemoteURL = %q, want %q", got.RemoteURL, testRemoteURL)
	}
	if got.LastPull != 0 || got.LastPush != 0 {
		t.Errorf("readMeta(missing) timestamps = %d/%d, want 0/0", got.LastPull, got.LastPush)
	}
}

func TestReadMeta_CorruptJSONReturnsDefault(t *testing.T) {
	c := &Cache{Dir: t.TempDir()}
	if err := os.MkdirAll(c.entryDir(testRemoteURL), 0o755); err != nil {
		t.Fatalf("mkdir entry: %v", err)
	}
	if err := os.WriteFile(c.metaPath(testRemoteURL), []byte("{not valid json"), 0o600); err != nil {
		t.Fatalf("write corrupt meta: %v", err)
	}
	got := c.readMeta(testRemoteURL)
	if got.RemoteURL != testRemoteURL {
		t.Errorf("readMeta(corrupt).RemoteURL = %q, want default %q", got.RemoteURL, testRemoteURL)
	}
}

func TestAcquireLock_SucceedsAndReleases(t *testing.T) {
	c := &Cache{Dir: t.TempDir()}
	if err := os.MkdirAll(c.entryDir(testRemoteURL), 0o755); err != nil {
		t.Fatalf("mkdir entry: %v", err)
	}
	f, err := c.acquireLock(context.Background(), testRemoteURL)
	if err != nil {
		t.Fatalf("acquireLock failed: %v", err)
	}
	if f == nil {
		t.Fatal("acquireLock returned nil file")
	}
	c.releaseLock(f)

	// After release, the lock is re-acquirable.
	f2, err := c.acquireLock(context.Background(), testRemoteURL)
	if err != nil {
		t.Fatalf("re-acquire after release failed: %v", err)
	}
	c.releaseLock(f2)
}

// TestAcquireLock_UnheldLeftoverFileIsAcquirable verifies that a leftover lock
// file with no live flock holder (e.g. from a crashed process) is immediately
// acquirable regardless of its mtime — flock is released by the OS on process
// death, so the try-lock succeeds without any mtime-based removal (beads-vw2m).
func TestAcquireLock_UnheldLeftoverFileIsAcquirable(t *testing.T) {
	c := &Cache{Dir: t.TempDir()}
	if err := os.MkdirAll(c.entryDir(testRemoteURL), 0o755); err != nil {
		t.Fatalf("mkdir entry: %v", err)
	}
	lp := c.lockPath(testRemoteURL)
	if err := os.WriteFile(lp, []byte("leftover"), 0o600); err != nil {
		t.Fatalf("seed leftover lock: %v", err)
	}
	// Backdate the lock file far into the past. Under the old mtime-based
	// cleanup this triggered a remove; now it is irrelevant — no one holds the
	// flock, so the try-lock acquires it directly.
	old := time.Now().Add(-24 * time.Hour)
	if err := os.Chtimes(lp, old, old); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	f, err := c.acquireLock(context.Background(), testRemoteURL)
	if err != nil {
		t.Fatalf("acquireLock over unheld leftover lock failed: %v", err)
	}
	c.releaseLock(f)
}

// TestAcquireLock_HeldLockNotStolenDespiteOldMtime is the beads-vw2m regression:
// a HELD lock whose file mtime is old (a long-running clone/pull does not
// refresh the mtime) must NOT be stealable by a second acquirer. The old
// mtime-based cleanup unlinked the held lock file, letting the second acquirer
// create+lock a new inode and run concurrently — corrupting the cache entry.
func TestAcquireLock_HeldLockNotStolenDespiteOldMtime(t *testing.T) {
	c := &Cache{Dir: t.TempDir()}
	if err := os.MkdirAll(c.entryDir(testRemoteURL), 0o755); err != nil {
		t.Fatalf("mkdir entry: %v", err)
	}
	held, err := c.acquireLock(context.Background(), testRemoteURL)
	if err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}
	defer c.releaseLock(held)

	// Simulate a long-running holder: backdate the lock file's mtime well past
	// the old 5-minute staleness window while the lock is still held.
	lp := c.lockPath(testRemoteURL)
	old := time.Now().Add(-24 * time.Hour)
	if err := os.Chtimes(lp, old, old); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	// A second acquire must NOT succeed (the held flock still guards the entry).
	// Use a cancelled context so we exercise the contended path without waiting
	// out the 2-minute deadline.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if f, err := c.acquireLock(ctx, testRemoteURL); err == nil {
		c.releaseLock(f)
		t.Fatal("second acquire stole a still-held lock with an old mtime — split-lock regression (beads-vw2m)")
	}

	// The lock file must still exist (never unlinked) so the holder's flock
	// remains meaningful.
	if _, statErr := os.Stat(lp); statErr != nil {
		t.Fatalf("held lock file was removed: %v", statErr)
	}
}

func TestAcquireLock_TimesOutWhenHeld(t *testing.T) {
	c := &Cache{Dir: t.TempDir()}
	if err := os.MkdirAll(c.entryDir(testRemoteURL), 0o755); err != nil {
		t.Fatalf("mkdir entry: %v", err)
	}
	// Hold the lock in this process.
	held, err := c.acquireLock(context.Background(), testRemoteURL)
	if err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}
	defer c.releaseLock(held)

	// A second acquire must abort via context cancellation (the poll loop
	// selects on ctx.Done()), exercising the interrupted-wait branch without
	// waiting out the 2-minute deadline.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.acquireLock(ctx, testRemoteURL); err == nil {
		t.Fatal("expected error acquiring an already-held lock with cancelled ctx")
	}
}

func TestEvict_RemovesEntry(t *testing.T) {
	c := &Cache{Dir: t.TempDir()}
	mkDoltDir(t, c, testRemoteURL)
	entry := c.entryDir(testRemoteURL)
	if _, err := os.Stat(entry); err != nil {
		t.Fatalf("entry should exist before Evict: %v", err)
	}
	if err := c.Evict(testRemoteURL); err != nil {
		t.Fatalf("Evict failed: %v", err)
	}
	if _, err := os.Stat(entry); !os.IsNotExist(err) {
		t.Errorf("entry should be gone after Evict, stat err = %v", err)
	}
}
