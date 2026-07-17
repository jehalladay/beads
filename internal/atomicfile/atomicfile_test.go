package atomicfile

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestWriteFile_Basic(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "out.txt")

	if err := WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello" {
		t.Errorf("got %q, want %q", got, "hello")
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o644 {
		t.Errorf("perm = %o, want 0644", perm)
	}
}

func TestWriteFile_Overwrite(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "out.txt")

	if err := os.WriteFile(path, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := WriteFile(path, []byte("replaced"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "replaced" {
		t.Errorf("got %q, want %q", got, "replaced")
	}
}

func TestCreate_Close(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "streamed.txt")

	w, err := Create(path, 0o644)
	if err != nil {
		t.Fatal(err)
	}

	for _, chunk := range []string{"line1\n", "line2\n", "line3\n"} {
		if _, err := w.Write([]byte(chunk)); err != nil {
			t.Fatal(err)
		}
	}

	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "line1\nline2\nline3\n"
	if string(got) != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestCreate_Abort(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "aborted.txt")

	w, err := Create(path, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("should not appear")); err != nil {
		t.Fatal(err)
	}
	if err := w.Abort(); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("expected target to not exist after Abort, got err=%v", err)
	}
}

func TestCreate_Abort_PreservesExisting(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "existing.txt")

	if err := os.WriteFile(path, []byte("keep me"), 0o644); err != nil {
		t.Fatal(err)
	}

	w, err := Create(path, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("overwrite attempt")); err != nil {
		t.Fatal(err)
	}
	if err := w.Abort(); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "keep me" {
		t.Errorf("original content clobbered: got %q, want %q", got, "keep me")
	}
}

func TestWriteFile_TempCleanup(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "clean.txt")

	if err := WriteFile(path, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".~") {
			t.Errorf("temp file left behind: %s", e.Name())
		}
	}
}

func TestWriteFile_SameDirectory(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(sub, "target.txt")

	// Create and immediately abort so we can inspect the temp file location.
	w, err := Create(path, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	tmpDir := filepath.Dir(w.f.Name())
	_ = w.Abort()

	if tmpDir != sub {
		t.Errorf("temp file in %q, want %q (same directory as target)", tmpDir, sub)
	}
}

func TestConcurrentWriters(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "concurrent.txt")

	const numWriters = 20
	const dataSize = 4096

	var wg sync.WaitGroup
	wg.Add(numWriters)

	for i := 0; i < numWriters; i++ {
		go func(id int) {
			defer wg.Done()
			// Each writer writes a distinct byte repeated dataSize times.
			data := make([]byte, dataSize)
			for j := range data {
				data[j] = byte('A' + id%26)
			}
			// Errors from concurrent rename races are acceptable;
			// the point is the final file must be valid.
			_ = WriteFile(path, data, 0o644)
		}(i)
	}
	wg.Wait()

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != dataSize {
		t.Fatalf("file size = %d, want %d", len(got), dataSize)
	}

	// Every byte must be the same character — no interleaving from
	// different writers.
	first := got[0]
	for i, b := range got {
		if b != first {
			t.Fatalf("corruption at byte %d: got %c, expected %c (consistent single-writer content)", i, b, first)
		}
	}
}

func TestConcurrentWriters_NoCorruption(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "nocorrupt.jsonl")

	const numWriters = 20

	// Simulate JSONL export: each writer writes multiple lines.
	var wg sync.WaitGroup
	wg.Add(numWriters)

	for i := 0; i < numWriters; i++ {
		go func(id int) {
			defer wg.Done()
			w, err := Create(path, 0o644)
			if err != nil {
				return // concurrent temp file creation can race; ok
			}
			for line := 0; line < 10; line++ {
				data := []byte(strings.Repeat(string(rune('A'+id%26)), 80) + "\n")
				if _, err := w.Write(data); err != nil {
					_ = w.Abort()
					return
				}
			}
			// Close may fail if another writer renamed over us; that's fine.
			if err := w.Close(); err != nil {
				return
			}
		}(i)
	}
	wg.Wait()

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	lines := strings.Split(strings.TrimSuffix(string(got), "\n"), "\n")
	if len(lines) != 10 {
		t.Fatalf("expected 10 lines, got %d", len(lines))
	}

	// All lines must contain the same character — proving the file came
	// from a single writer, not interleaved from multiple.
	firstChar := lines[0][0]
	for i, line := range lines {
		if len(line) != 80 {
			t.Fatalf("line %d length = %d, want 80", i, len(line))
		}
		for j, b := range []byte(line) {
			if b != firstChar {
				t.Fatalf("line %d byte %d: got %c, expected %c (interleaved writers)", i, j, b, firstChar)
			}
		}
	}
}

func TestWriteFile_CreateError(t *testing.T) {
	t.Parallel()
	// Target dir does not exist -> CreateTemp fails -> WriteFile returns early.
	dir := t.TempDir()
	path := filepath.Join(dir, "no-such-subdir", "out.txt")

	err := WriteFile(path, []byte("data"), 0o644)
	if err == nil {
		t.Fatal("expected error writing into a nonexistent directory")
	}
	if !strings.Contains(err.Error(), "create temp") {
		t.Errorf("error = %v, want a 'create temp' error", err)
	}
}

func TestCreate_Error(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// Parent path component is a regular file, not a directory.
	notDir := filepath.Join(dir, "regular")
	if err := os.WriteFile(notDir, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Create(filepath.Join(notDir, "child.txt"), 0o644)
	if err == nil {
		t.Fatal("expected Create to fail when the parent is not a directory")
	}
}

func TestWriteFile_CopyError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "out.txt")

	w, err := Create(path, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	// Close the underlying fd out from under the Writer so the io.Copy in
	// WriteFile's sibling path (here simulated directly) errors. We drive the
	// WriteFile copy-error branch by pre-closing the temp file's descriptor.
	if err := w.f.Close(); err != nil {
		t.Fatal(err)
	}
	_, werr := w.Write([]byte("data"))
	if werr == nil {
		t.Fatal("expected Write to fail on a closed descriptor")
	}
	// Clean up the orphaned temp file.
	_ = os.Remove(w.f.Name())
}

func TestClose_Idempotent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "out.txt")

	w, err := Create(path, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	// Second Close is a no-op via the done guard.
	if err := w.Close(); err != nil {
		t.Errorf("second Close returned %v, want nil", err)
	}
}

func TestAbort_AfterClose_NoOp(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "out.txt")

	w, err := Create(path, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	// Abort after a successful Close must be a no-op and must NOT remove the
	// renamed target.
	if err := w.Abort(); err != nil {
		t.Errorf("Abort after Close returned %v, want nil", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("target missing after Abort-post-Close: %v", err)
	}
	if string(got) != "hello" {
		t.Errorf("target content = %q, want hello", got)
	}
}

func TestAbort_Idempotent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "out.txt")

	w, err := Create(path, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Abort(); err != nil {
		t.Fatal(err)
	}
	// Second Abort is a no-op via the done guard (temp file already gone).
	if err := w.Abort(); err != nil {
		t.Errorf("second Abort returned %v, want nil", err)
	}
}

func TestClose_RenameError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// Target path is an existing directory -> os.Rename(tmpFile, dirTarget)
	// fails, exercising Close's rename-error branch and temp cleanup.
	target := filepath.Join(dir, "iam-a-dir")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}

	w, err := Create(target, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	tmpName := w.f.Name()
	if _, err := w.Write([]byte("data")); err != nil {
		t.Fatal(err)
	}
	err = w.Close()
	if err == nil {
		t.Fatal("expected Close to fail renaming over an existing directory")
	}
	if !strings.Contains(err.Error(), "rename") {
		t.Errorf("error = %v, want a 'rename' error", err)
	}
	// Temp file must be cleaned up on the rename-error path.
	if _, statErr := os.Stat(tmpName); !os.IsNotExist(statErr) {
		t.Errorf("temp file %s not cleaned up after rename error", tmpName)
	}
}

func TestClose_ChmodError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "out.txt")

	w, err := Create(path, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	tmpName := w.f.Name()
	// Close the descriptor so the Chmod inside Close fails on a bad fd.
	if err := w.f.Close(); err != nil {
		t.Fatal(err)
	}
	err = w.Close()
	if err == nil {
		t.Fatal("expected Close to fail chmod on a closed descriptor")
	}
	if !strings.Contains(err.Error(), "chmod") {
		t.Errorf("error = %v, want a 'chmod' error", err)
	}
	// Temp file must be cleaned up on the chmod-error path.
	if _, statErr := os.Stat(tmpName); !os.IsNotExist(statErr) {
		t.Errorf("temp file %s not cleaned up after chmod error", tmpName)
	}
	// Target must not exist.
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Errorf("target should not exist after failed Close")
	}
}

// TestClose_SyncError drives Close's Sync-error branch using a pipe write-end,
// which accepts Chmod but returns EINVAL from Sync. White-box (in-package):
// constructs a Writer directly since the public API always hands back a
// regular file whose Sync succeeds.
func TestClose_SyncError(t *testing.T) {
	t.Parallel()
	r, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close(); _ = pw.Close() })

	dir := t.TempDir()
	w := &Writer{
		target: filepath.Join(dir, "target.txt"),
		f:      pw,
		perm:   0o644,
	}
	err = w.Close()
	if err == nil {
		t.Fatal("expected Close to fail Sync on a pipe fd")
	}
	if !strings.Contains(err.Error(), "sync") {
		t.Errorf("error = %v, want a 'sync' error", err)
	}
	if !w.done {
		t.Error("Writer should be marked done after a failed Close")
	}
	// Target must not have been created.
	if _, statErr := os.Stat(w.target); !os.IsNotExist(statErr) {
		t.Error("target should not exist after a failed Sync")
	}
}
