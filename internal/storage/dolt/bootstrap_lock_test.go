package dolt

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestAcquireBootstrapLock_AcquiresAndReleases verifies a lock can be acquired
// and, after release, re-acquired (beads-apb1).
func TestAcquireBootstrapLock_AcquiresAndReleases(t *testing.T) {
	dir := t.TempDir()
	lp := filepath.Join(dir, "dolt.bootstrap.lock")

	f, err := acquireBootstrapLock(lp, 2*time.Second)
	if err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}
	releaseBootstrapLock(f, lp)

	f2, err := acquireBootstrapLock(lp, 2*time.Second)
	if err != nil {
		t.Fatalf("re-acquire after release failed: %v", err)
	}
	releaseBootstrapLock(f2, lp)
}

// TestAcquireBootstrapLock_HeldNotStolenDespiteOldMtime is the beads-vw2m /
// beads-apb1 regression: a HELD lock whose file mtime is old (a long clone does
// not refresh the mtime) must NOT be stealable by a second acquirer, and its
// lock file must not be unlinked.
func TestAcquireBootstrapLock_HeldNotStolenDespiteOldMtime(t *testing.T) {
	dir := t.TempDir()
	lp := filepath.Join(dir, "dolt.bootstrap.lock")

	held, err := acquireBootstrapLock(lp, 2*time.Second)
	if err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}
	defer releaseBootstrapLock(held, lp)

	// Backdate the lock file's mtime far past the old 5-minute staleness window
	// while it is still held.
	old := time.Now().Add(-24 * time.Hour)
	if err := os.Chtimes(lp, old, old); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	// A second acquire with a short timeout must fail (the held flock guards it),
	// NOT steal the lock by unlinking the "stale" file.
	if f, err := acquireBootstrapLock(lp, 300*time.Millisecond); err == nil {
		releaseBootstrapLock(f, lp)
		t.Fatal("second acquire stole a still-held lock with an old mtime — split-lock regression (beads-apb1/vw2m)")
	}

	// The held lock file must still exist (release must not have run; and no
	// stale-cleanup should have unlinked it).
	if _, statErr := os.Stat(lp); statErr != nil {
		t.Fatalf("held lock file was removed: %v", statErr)
	}
}

// TestReleaseBootstrapLock_DoesNotUnlink verifies release leaves the lock file
// in place (unlinking it is the TOCTOU hazard beads-vw2m documents).
func TestReleaseBootstrapLock_DoesNotUnlink(t *testing.T) {
	dir := t.TempDir()
	lp := filepath.Join(dir, "dolt.bootstrap.lock")

	f, err := acquireBootstrapLock(lp, 2*time.Second)
	if err != nil {
		t.Fatalf("acquire failed: %v", err)
	}
	releaseBootstrapLock(f, lp)

	if _, statErr := os.Stat(lp); statErr != nil {
		t.Fatalf("release unlinked the lock file (TOCTOU hazard): %v", statErr)
	}
}
