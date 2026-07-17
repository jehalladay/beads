package atomicfile

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestSweepStale_RemovesOldOrphan is the teeth for beads-qoda: a process crash
// between Create and Close/Abort leaves an orphaned ".~<base>.<rand>" temp in
// the target directory, and nothing sweeps it. SweepStale must remove a temp
// older than the age threshold.
func TestSweepStale_RemovesOldOrphan(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Simulate a crash-orphaned temp: an atomicfile temp name that was never
	// renamed or removed.
	orphan := filepath.Join(dir, ".~data.txt.abc123")
	if err := os.WriteFile(orphan, []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Backdate its mtime well past the threshold.
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(orphan, old, old); err != nil {
		t.Fatal(err)
	}

	n, err := SweepStale(dir, time.Hour)
	if err != nil {
		t.Fatalf("SweepStale: %v", err)
	}
	if n != 1 {
		t.Errorf("swept %d, want 1", n)
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Errorf("stale orphan not removed: stat err = %v", err)
	}
}

// TestSweepStale_PreservesFreshTemp guards against the race that would make the
// sweep dangerous: a temp from a CONCURRENT live write (recent mtime) must NOT
// be removed, or the sweep would corrupt an in-flight atomic write.
func TestSweepStale_PreservesFreshTemp(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	fresh := filepath.Join(dir, ".~data.txt.xyz789")
	if err := os.WriteFile(fresh, []byte("in-flight"), 0o600); err != nil {
		t.Fatal(err)
	}
	// mtime is "now" (fresh) — inside the threshold, so it must survive.

	n, err := SweepStale(dir, time.Hour)
	if err != nil {
		t.Fatalf("SweepStale: %v", err)
	}
	if n != 0 {
		t.Errorf("swept %d fresh temps, want 0 (would corrupt a live write)", n)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Errorf("fresh temp was removed: %v", err)
	}
}

// TestSweepStale_IgnoresNonTempFiles confirms the sweep only touches its own
// ".~<base>." temp files and never a real file (even an old one).
func TestSweepStale_IgnoresNonTempFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	real := filepath.Join(dir, "data.txt")
	if err := os.WriteFile(real, []byte("keep me"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Old but not a temp — must never be swept.
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(real, old, old); err != nil {
		t.Fatal(err)
	}
	// A hidden dotfile that is not an atomicfile temp (no ".~" prefix).
	dot := filepath.Join(dir, ".config")
	if err := os.WriteFile(dot, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(dot, old, old); err != nil {
		t.Fatal(err)
	}

	n, err := SweepStale(dir, time.Hour)
	if err != nil {
		t.Fatalf("SweepStale: %v", err)
	}
	if n != 0 {
		t.Errorf("swept %d non-temp files, want 0", n)
	}
	if _, err := os.Stat(real); err != nil {
		t.Errorf("real file removed: %v", err)
	}
	if _, err := os.Stat(dot); err != nil {
		t.Errorf("non-temp dotfile removed: %v", err)
	}
}

// TestSweepStale_SkipsSubdirectories confirms the sweep does not descend into
// or remove directories, even one that happens to match the temp prefix.
func TestSweepStale_SkipsSubdirectories(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	sub := filepath.Join(dir, ".~data.txt.dir")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(sub, old, old); err != nil {
		t.Fatal(err)
	}

	n, err := SweepStale(dir, time.Hour)
	if err != nil {
		t.Fatalf("SweepStale: %v", err)
	}
	if n != 0 {
		t.Errorf("swept %d directories, want 0", n)
	}
	if _, err := os.Stat(sub); err != nil {
		t.Errorf("subdirectory removed: %v", err)
	}
}

// TestSweepStale_MissingDirIsNoError confirms sweeping a nonexistent directory
// is a benign no-op (callers may sweep a dir that was never written to).
func TestSweepStale_MissingDirIsNoError(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "does-not-exist")

	n, err := SweepStale(dir, time.Hour)
	if err != nil {
		t.Errorf("SweepStale on missing dir = %v, want nil", err)
	}
	if n != 0 {
		t.Errorf("swept %d in missing dir, want 0", n)
	}
}
