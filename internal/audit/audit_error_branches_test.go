package audit

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestEnsureFile_PathError exercises the branch where Path() fails (no .beads
// dir discoverable): EnsureFile must propagate the error and create nothing.
func TestEnsureFile_PathError(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("BEADS_DIR", filepath.Join(tmp, "does-not-exist"))
	t.Chdir(tmp)

	if _, err := EnsureFile(); err == nil {
		t.Fatal("EnsureFile() should error when Path() cannot find a .beads dir")
	}
}

// TestEnsureFile_OpenError covers the non-ErrExist OpenFile failure branch: a
// read-only .beads directory rejects O_CREATE, producing a permission error
// that is neither nil nor os.ErrExist.
func TestEnsureFile_OpenError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory permission bits")
	}
	tmp := t.TempDir()
	beadsDir := filepath.Join(tmp, ".beads")
	if err := os.MkdirAll(beadsDir, 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(beadsDir, "metadata.json"), []byte(`{"backend":"dolt"}`), 0644); err != nil {
		t.Fatalf("write metadata.json: %v", err)
	}
	t.Setenv("BEADS_DIR", beadsDir)
	// Strip write permission so O_CREATE|O_EXCL fails with EACCES (not ErrExist).
	if err := os.Chmod(beadsDir, 0500); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	// Restore perms so t.TempDir cleanup can remove the tree.
	defer func() { _ = os.Chmod(beadsDir, 0700) }()

	_, err := EnsureFile()
	if err == nil {
		t.Fatal("EnsureFile() should error when the log cannot be created in a read-only dir")
	}
	if errors.Is(err, os.ErrExist) {
		t.Fatalf("error should not be ErrExist, got %v", err)
	}
}

// TestEnsureFile_AlreadyExists covers the ErrExist branch: a pre-existing log
// file is left intact and its path returned with no error.
func TestEnsureFile_AlreadyExists(t *testing.T) {
	path := setupBeadsDir(t)
	seed := []byte("{\"id\":\"pre-existing\"}\n")
	if err := os.WriteFile(path, seed, 0644); err != nil {
		t.Fatalf("seed log: %v", err)
	}

	got, err := EnsureFile()
	if err != nil {
		t.Fatalf("EnsureFile() on existing file: %v", err)
	}
	if got != path {
		t.Errorf("EnsureFile() = %q, want %q", got, path)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if string(after) != string(seed) {
		t.Errorf("EnsureFile() truncated existing log: got %q, want %q", after, seed)
	}
}

// TestAppend_EnsureFileError covers Append's propagation of an EnsureFile
// error (Path fails → no file written, error returned).
func TestAppend_EnsureFileError(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("BEADS_DIR", filepath.Join(tmp, "does-not-exist"))
	t.Chdir(tmp)

	if _, err := Append(&Entry{Kind: "llm_call"}); err == nil {
		t.Fatal("Append() should error when EnsureFile() fails")
	}
}

// TestAppend_NewIDError covers the branch where a caller supplies no ID and
// newID's entropy source fails. Append must return the error and write nothing.
func TestAppend_NewIDError(t *testing.T) {
	path := setupBeadsDir(t)

	oldRead := randRead
	defer func() { randRead = oldRead }()
	sentinel := errors.New("no entropy")
	randRead = func([]byte) (int, error) { return 0, sentinel }

	_, err := Append(&Entry{Kind: "llm_call"})
	if err == nil {
		t.Fatal("Append() should error when newID() fails")
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("error should wrap the entropy failure, got %v", err)
	}
	// Nothing should have been appended (file may exist but must be empty).
	if entries := readEntries(t, path); len(entries) != 0 {
		t.Fatalf("expected no entries after a newID failure, got %d", len(entries))
	}
}

// TestNewID_Success sanity-checks the happy path (prefix + 32 hex chars).
func TestNewID_Success(t *testing.T) {
	id, err := newID()
	if err != nil {
		t.Fatalf("newID: %v", err)
	}
	if len(id) != len(idPrefix)+32 {
		t.Fatalf("newID() = %q, want %d chars", id, len(idPrefix)+32)
	}
}
