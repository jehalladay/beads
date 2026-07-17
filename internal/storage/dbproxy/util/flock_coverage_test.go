package util

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These tests fill TryLock's two error-return branches that the acquire/
// contention tests don't reach: failure to create the lock directory, and
// failure to open the lock file. Both are provoked hermetically (t.TempDir).

func TestTryLock_MkdirAllError(t *testing.T) {
	// A regular file sitting where TryLock needs a directory makes
	// os.MkdirAll fail (ENOTDIR), exercising the "creating lock directory"
	// error branch.
	dir := t.TempDir()
	notADir := filepath.Join(dir, "afile")
	if err := os.WriteFile(notADir, []byte("x"), 0o600); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	// lockPath = afile/sub/beads.lock → Dir = afile/sub, and afile is a file,
	// so MkdirAll can't create afile/sub.
	lockPath := filepath.Join(notADir, "sub", "beads.lock")
	l, err := TryLock(lockPath)
	if err == nil {
		if l != nil {
			l.Unlock()
		}
		t.Fatal("TryLock with a non-directory path component err = nil, want a mkdir error")
	}
	if l != nil {
		t.Errorf("TryLock returned a non-nil lock alongside an error: %+v", l)
	}
	if !strings.Contains(err.Error(), "creating lock directory") {
		t.Errorf("error = %q, want it to mention creating lock directory", err)
	}
}

func TestTryLock_OpenFileError(t *testing.T) {
	// When lockPath is itself an existing directory, MkdirAll(Dir(lockPath))
	// succeeds but os.OpenFile with O_RDWR fails (EISDIR), exercising the
	// "opening lock file" error branch.
	dir := t.TempDir()
	lockPath := filepath.Join(dir, "iamadir")
	if err := os.Mkdir(lockPath, 0o700); err != nil {
		t.Fatalf("seed dir: %v", err)
	}
	l, err := TryLock(lockPath)
	if err == nil {
		if l != nil {
			l.Unlock()
		}
		t.Fatal("TryLock on a directory path err = nil, want an open error")
	}
	if l != nil {
		t.Errorf("TryLock returned a non-nil lock alongside an error: %+v", l)
	}
	if !strings.Contains(err.Error(), "opening lock file") {
		t.Errorf("error = %q, want it to mention opening lock file", err)
	}
}
