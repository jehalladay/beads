package remotecache

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// These tests exercise the error/failure branches of the cache and URL helpers
// that the happy-path integration tests (which require the dolt CLI) miss.
// Most are fully hermetic (no dolt); the clone/pull-failure cases drive the real
// dolt binary against a bogus file:// remote so it exits non-zero fast.

// TestEnsure_InvalidURL covers the ValidateRemoteURL rejection branch in Ensure:
// a malformed URL must fail before any dolt lookup or filesystem work.
func TestEnsure_InvalidURL(t *testing.T) {
	c := &Cache{Dir: t.TempDir()}
	if _, err := c.Ensure(context.Background(), "-flaginjection"); err == nil {
		t.Fatal("expected Ensure to reject a URL that starts with a dash")
	}
	// An empty URL is also invalid.
	if _, err := c.Ensure(context.Background(), ""); err == nil {
		t.Fatal("expected Ensure to reject an empty URL")
	}
}

// TestEnsure_CloneFailure covers Ensure's cold-start clone-error branch and,
// transitively, doltClone's failure path: a structurally-valid but bogus
// file:// remote makes `dolt clone` exit non-zero.
func TestEnsure_CloneFailure(t *testing.T) {
	skipIfNoDolt(t)
	c := &Cache{Dir: t.TempDir()}
	// Valid file:// URL (passes ValidateRemoteURL) pointing at a non-existent
	// remote store, so dolt clone fails.
	bogus := "file://" + filepath.Join(t.TempDir(), "does-not-exist")
	_, err := c.Ensure(context.Background(), bogus)
	if err == nil {
		t.Fatal("expected Ensure to surface a dolt clone failure")
	}
}

// TestPush_NoCachedClone covers Push's guard when the clone is absent.
func TestPush_NoCachedClone(t *testing.T) {
	c := &Cache{Dir: t.TempDir()}
	if err := c.Push(context.Background(), testRemoteURL); err == nil {
		t.Fatal("expected Push to fail when no cached clone exists")
	}
}

// TestPull_Failure covers Ensure's warm-start pull-error branch (and doltPull's
// failure path): we fabricate a .dolt directory so doltExists reports true and
// Ensure takes the pull branch, but the directory is not a real dolt repo so
// `dolt pull` exits non-zero.
func TestPull_Failure(t *testing.T) {
	skipIfNoDolt(t)
	c := &Cache{Dir: t.TempDir()}
	// Fabricate a fake clone: <target>/.dolt exists but is not a valid repo.
	mkDoltDir(t, c, testRemoteURL)
	// FreshFor default 0 on the zero-value Cache means Ensure always pulls.
	_, err := c.Ensure(context.Background(), testRemoteURL)
	if err == nil {
		t.Fatal("expected Ensure to surface a dolt pull failure on a fake clone")
	}
}

// TestWriteMeta_UnwritablePath covers writeMeta's os.WriteFile error branch.
// writeMeta logs and returns on failure (best-effort); we assert it does not
// panic and does not create the meta file when the entry dir is missing.
func TestWriteMeta_UnwritablePath(t *testing.T) {
	c := &Cache{Dir: t.TempDir()}
	// Deliberately do NOT create the entry dir, so metaPath's parent is absent
	// and os.WriteFile fails with ENOENT.
	c.writeMeta(testRemoteURL, &CacheMeta{RemoteURL: testRemoteURL, LastPull: 1})
	if _, err := os.Stat(c.metaPath(testRemoteURL)); !os.IsNotExist(err) {
		t.Errorf("meta file should not exist after a failed write, stat err = %v", err)
	}
}

// TestAcquireLock_OpenFileError covers acquireLock's os.OpenFile error branch:
// when the entry dir does not exist, opening the lock file returns an error
// (not a lock-contention error), so acquireLock returns it directly.
func TestAcquireLock_OpenFileError(t *testing.T) {
	c := &Cache{Dir: t.TempDir()}
	// Entry dir intentionally absent → lock path's parent does not exist.
	f, err := c.acquireLock(context.Background(), testRemoteURL)
	if err == nil {
		if f != nil {
			c.releaseLock(f)
		}
		t.Fatal("expected acquireLock to fail when the entry dir is missing")
	}
	if f != nil {
		t.Errorf("acquireLock should return a nil file on error, got %v", f)
	}
}

// TestReleaseLock_NilIsSafe covers releaseLock's nil-guard.
func TestReleaseLock_NilIsSafe(t *testing.T) {
	c := &Cache{Dir: t.TempDir()}
	c.releaseLock(nil) // must not panic
}

// TestMatchesRemotePattern_InvalidPattern covers MatchesRemotePattern's
// error branch: a syntactically-invalid glob makes path.Match return an
// error, and the function must return false (not panic). The existing
// TestMatchesRemotePattern in url_test.go only exercises match/no-match.
func TestMatchesRemotePattern_InvalidPattern(t *testing.T) {
	// "[" is an unterminated character class → path.Match syntax error.
	if MatchesRemotePattern("dolthub://org/repo", "[") {
		t.Error("MatchesRemotePattern should return false when the pattern is invalid")
	}
}
