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

func TestAcquireLock_StaleLockCleanedUp(t *testing.T) {
	c := &Cache{Dir: t.TempDir()}
	if err := os.MkdirAll(c.entryDir(testRemoteURL), 0o755); err != nil {
		t.Fatalf("mkdir entry: %v", err)
	}
	lp := c.lockPath(testRemoteURL)
	if err := os.WriteFile(lp, []byte("stale"), 0o600); err != nil {
		t.Fatalf("seed stale lock: %v", err)
	}
	// Backdate the lock file well beyond staleLockAge so acquireLock removes it.
	old := time.Now().Add(-2 * staleLockAge)
	if err := os.Chtimes(lp, old, old); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	f, err := c.acquireLock(context.Background(), testRemoteURL)
	if err != nil {
		t.Fatalf("acquireLock over stale lock failed: %v", err)
	}
	c.releaseLock(f)
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
