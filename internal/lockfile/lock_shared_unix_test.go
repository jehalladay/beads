//go:build unix

package lockfile

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// openLock creates and opens a fresh lock file under a temp dir, returning an
// O_RDWR handle. Each call yields a distinct open-file-description, which is the
// granularity flock(2) contends on, so two handles to the same path model two
// independent lock holders (as the exclusive-lock tests already rely on).
func openLock(t *testing.T) *os.File {
	t.Helper()
	path := filepath.Join(t.TempDir(), "shared.lock")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatalf("create lock file: %v", err)
	}
	f, err := os.OpenFile(path, os.O_RDWR, 0o644)
	if err != nil {
		t.Fatalf("open lock file: %v", err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

// openSame opens an additional independent handle to an already-open lock
// file's path, modeling a second holder contending for the same file.
func openSame(t *testing.T, first *os.File) *os.File {
	t.Helper()
	f, err := os.OpenFile(first.Name(), os.O_RDWR, 0o644)
	if err != nil {
		t.Fatalf("open second handle: %v", err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

func TestFlockSharedNonBlock_SucceedsOnUnlocked(t *testing.T) {
	f := openLock(t)
	if err := FlockSharedNonBlock(f); err != nil {
		t.Fatalf("FlockSharedNonBlock on unlocked file = %v, want nil", err)
	}
	if err := FlockUnlock(f); err != nil {
		t.Errorf("FlockUnlock = %v, want nil", err)
	}
}

func TestFlockSharedNonBlock_ConcurrentSharedBothSucceed(t *testing.T) {
	f1 := openLock(t)
	if err := FlockSharedNonBlock(f1); err != nil {
		t.Fatalf("first shared lock = %v, want nil", err)
	}
	defer FlockUnlock(f1)

	// A second shared lock must coexist with the first — that is the whole
	// point of a shared (read) lock.
	f2 := openSame(t, f1)
	if err := FlockSharedNonBlock(f2); err != nil {
		t.Errorf("second concurrent shared lock = %v, want nil", err)
	}
	defer FlockUnlock(f2)
}

func TestFlockSharedNonBlock_BusyWhenExclusiveHeld(t *testing.T) {
	f1 := openLock(t)
	if err := FlockExclusiveBlocking(f1); err != nil {
		t.Fatalf("acquire exclusive lock: %v", err)
	}
	defer FlockUnlock(f1)

	f2 := openSame(t, f1)
	err := FlockSharedNonBlock(f2)
	if !errors.Is(err, ErrLockBusy) {
		t.Errorf("shared lock while exclusive held = %v, want ErrLockBusy", err)
	}
}

func TestFlockExclusiveNonBlock_SucceedsOnUnlocked(t *testing.T) {
	f := openLock(t)
	if err := FlockExclusiveNonBlock(f); err != nil {
		t.Fatalf("FlockExclusiveNonBlock on unlocked file = %v, want nil", err)
	}
	if err := FlockUnlock(f); err != nil {
		t.Errorf("FlockUnlock = %v, want nil", err)
	}
}

func TestFlockExclusiveNonBlock_BusyWhenContended(t *testing.T) {
	tests := []struct {
		name string
		hold func(*os.File) error // how the first handle takes the lock
	}{
		{name: "shared held", hold: FlockSharedNonBlock},
		{name: "exclusive held", hold: FlockExclusiveBlocking},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f1 := openLock(t)
			if err := tt.hold(f1); err != nil {
				t.Fatalf("first lock (%s) = %v, want nil", tt.name, err)
			}
			defer FlockUnlock(f1)

			f2 := openSame(t, f1)
			err := FlockExclusiveNonBlock(f2)
			if !errors.Is(err, ErrLockBusy) {
				t.Errorf("exclusive lock while %s = %v, want ErrLockBusy", tt.name, err)
			}
		})
	}
}

// TestFlockSharedNonBlock_AcquirableAfterRelease confirms the busy state clears
// once the conflicting exclusive lock is released, so ErrLockBusy is transient
// rather than sticky.
func TestFlockSharedNonBlock_AcquirableAfterRelease(t *testing.T) {
	f1 := openLock(t)
	if err := FlockExclusiveBlocking(f1); err != nil {
		t.Fatalf("acquire exclusive lock: %v", err)
	}

	f2 := openSame(t, f1)
	if err := FlockSharedNonBlock(f2); !errors.Is(err, ErrLockBusy) {
		t.Fatalf("precondition: expected ErrLockBusy, got %v", err)
	}

	if err := FlockUnlock(f1); err != nil {
		t.Fatalf("release exclusive lock: %v", err)
	}
	if err := FlockSharedNonBlock(f2); err != nil {
		t.Errorf("shared lock after release = %v, want nil", err)
	}
	defer FlockUnlock(f2)
}
