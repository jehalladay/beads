package pidfile

import (
	"os"
	"path/filepath"
	"testing"
)

// These tests fill the branches the round-trip tests don't reach: Read on a
// missing/corrupt file, Remove across its three outcomes, and Path. All
// hermetic (t.TempDir, no live proxy).

func TestPath(t *testing.T) {
	got := Path("/some/root", "proxy.pid")
	want := filepath.Join("/some/root", "proxy.pid")
	if got != want {
		t.Errorf("Path = %q, want %q", got, want)
	}
}

func TestRead_NotExist(t *testing.T) {
	dir := t.TempDir()
	// A missing pidfile is not an error: Read returns (nil, nil) so callers
	// can treat "no proxy running" as a normal state.
	pf, err := Read(dir, "absent.pid")
	if err != nil {
		t.Fatalf("Read of missing file err = %v, want nil", err)
	}
	if pf != nil {
		t.Errorf("Read of missing file = %+v, want nil", pf)
	}
}

func TestRead_BadJSON(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(Path(dir, "corrupt.pid"), []byte("{not json"), 0o644); err != nil {
		t.Fatalf("seed corrupt file: %v", err)
	}
	pf, err := Read(dir, "corrupt.pid")
	if err == nil {
		t.Fatalf("Read of corrupt JSON err = nil, want a parse error (pf=%+v)", pf)
	}
	if pf != nil {
		t.Errorf("Read of corrupt JSON = %+v, want nil", pf)
	}
}

func TestRead_NonENOENTError(t *testing.T) {
	// os.ReadFile on a path whose parent is a regular file (not a directory)
	// returns a non-ENOENT error, exercising Read's error-return branch (the
	// one that is neither "missing → nil,nil" nor a JSON parse failure).
	dir := t.TempDir()
	notADir := filepath.Join(dir, "afile")
	if err := os.WriteFile(notADir, []byte("x"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	// rootDir = notADir (a file); name = "child" → path "afile/child".
	pf, err := Read(notADir, "child")
	if err == nil {
		t.Fatalf("Read under a non-directory root err = nil, want a non-ENOENT error (pf=%+v)", pf)
	}
	if os.IsNotExist(err) {
		t.Errorf("Read err classified as NotExist, want a different error: %v", err)
	}
	if pf != nil {
		t.Errorf("Read under a non-directory root = %+v, want nil", pf)
	}
}

func TestRemove_Nonexistent(t *testing.T) {
	dir := t.TempDir()
	// Removing an absent pidfile is a no-op, not an error (idempotent
	// cleanup).
	if err := Remove(dir, "absent.pid"); err != nil {
		t.Errorf("Remove of missing file err = %v, want nil", err)
	}
}

func TestRemove_Existing(t *testing.T) {
	dir := t.TempDir()
	if err := Write(dir, "proxy.pid", PidFile{Pid: 1, Port: 2}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := Remove(dir, "proxy.pid"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, statErr := os.Stat(Path(dir, "proxy.pid")); !os.IsNotExist(statErr) {
		t.Errorf("pidfile still present after Remove: stat err = %v", statErr)
	}
	// Second Remove is still a no-op.
	if err := Remove(dir, "proxy.pid"); err != nil {
		t.Errorf("second Remove err = %v, want nil", err)
	}
}

func TestRemove_NonENOENTError(t *testing.T) {
	// os.Remove on a path whose parent is a regular file (not a directory)
	// returns a non-ENOENT error, exercising Remove's error-return branch.
	dir := t.TempDir()
	notADir := filepath.Join(dir, "afile")
	if err := os.WriteFile(notADir, []byte("x"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	// rootDir = notADir (a file); name = "child" → path "afile/child".
	err := Remove(notADir, "child")
	if err == nil {
		t.Fatal("Remove under a non-directory root err = nil, want a non-ENOENT error")
	}
	if os.IsNotExist(err) {
		t.Errorf("Remove err classified as NotExist, want a different error: %v", err)
	}
}
