package atomicfile

import (
	"os"
	"path/filepath"
	"testing"
)

// TestSyncDir_RealDirectory verifies syncDir succeeds on an existing directory
// (a no-op success on non-Unix). This is the durability step added for the
// atomicfile crash-durability contract (parent-dir fsync after rename).
func TestSyncDir_RealDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := syncDir(dir); err != nil {
		t.Fatalf("syncDir(%q) = %v, want nil", dir, err)
	}
}

// TestWriteFile_LandsAfterDirSync is an end-to-end check that adding the
// parent-dir fsync to Close did not break the happy path: the file exists with
// exactly the written bytes and the correct mode, and no temp file is left in
// the directory.
func TestWriteFile_LandsAfterDirSync(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "data.txt")
	want := []byte("durable payload")

	if err := WriteFile(target, want, 0o640); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("content = %q, want %q", got, want)
	}

	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if runtimeSupportsChmod() && info.Mode().Perm() != 0o640 {
		t.Fatalf("mode = %v, want 0640", info.Mode().Perm())
	}

	// No temp files left behind (temp names are ".~<base>.<random>").
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if e.Name() != "data.txt" {
			t.Fatalf("unexpected leftover file %q in target dir", e.Name())
		}
	}
}

// runtimeSupportsChmod reports whether the platform honors file mode bits.
// Windows applies a restricted subset, so the mode assertion is skipped there.
func runtimeSupportsChmod() bool {
	return os.PathSeparator == '/'
}
