// Package atomicfile provides atomic file writes via temp-file + rename.
//
// Writes land in a temporary file in the same directory as the target,
// are fsynced, then atomically renamed into place. Readers never see a
// partial or truncated file — only the previous complete version or the
// new complete version.
package atomicfile

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// WriteFile atomically writes data to path with the given permissions.
// It creates an atomic Writer, copies data in via io.Copy, then
// fsyncs and renames into place.
func WriteFile(path string, data []byte, perm os.FileMode) error {
	w, err := Create(path, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(w, bytes.NewReader(data)); err != nil {
		_ = w.Abort()
		return err
	}
	return w.Close()
}

// Writer is an io.WriteCloser that writes to a temporary file and
// atomically renames it to the target path on Close. Call Abort to
// discard the temp file without touching the target.
type Writer struct {
	target string
	f      *os.File
	perm   os.FileMode
	done   bool
}

// Create returns a Writer that will atomically replace path on Close.
// The temp file is created in the same directory as path to guarantee
// same-filesystem rename semantics.
func Create(path string, perm os.FileMode) (*Writer, error) {
	dir := filepath.Dir(path)
	base := filepath.Base(path)

	f, err := os.CreateTemp(dir, ".~"+base+".")
	if err != nil {
		return nil, fmt.Errorf("atomicfile: create temp: %w", err)
	}

	return &Writer{
		target: path,
		f:      f,
		perm:   perm,
	}, nil
}

// Write delegates to the underlying temp file.
func (w *Writer) Write(p []byte) (int, error) {
	return w.f.Write(p)
}

// Close fsyncs the temp file and atomically renames it to the target path.
// After Close returns successfully, the target contains exactly the data
// written. On error the temp file is removed and the target is untouched.
func (w *Writer) Close() error {
	if w.done {
		return nil
	}
	w.done = true

	// Ensure permissions before rename — CreateTemp uses 0600 by default.
	if err := w.f.Chmod(w.perm); err != nil {
		_ = w.f.Close()
		_ = os.Remove(w.f.Name())
		return fmt.Errorf("atomicfile: chmod: %w", err)
	}

	if err := w.f.Sync(); err != nil {
		_ = w.f.Close()
		_ = os.Remove(w.f.Name())
		return fmt.Errorf("atomicfile: sync: %w", err)
	}

	if err := w.f.Close(); err != nil {
		_ = os.Remove(w.f.Name())
		return fmt.Errorf("atomicfile: close: %w", err)
	}

	if err := os.Rename(w.f.Name(), w.target); err != nil {
		_ = os.Remove(w.f.Name())
		return fmt.Errorf("atomicfile: rename: %w", err)
	}

	// fsync the parent directory so the rename itself is durable: a successful
	// os.Rename makes the new name visible to concurrent readers, but the
	// directory entry is not persisted until the directory is fsynced. Without
	// this a crash right after rename can lose the entry entirely (neither the
	// old nor the new file survives), breaking the crash-durability contract in
	// this package's doc comment. The data is already renamed into place, so a
	// dir-sync failure is a durability-not-correctness concern; surface it so
	// callers relying on persistence can react. No-op on non-Unix (see syncDir).
	if err := syncDir(filepath.Dir(w.target)); err != nil {
		return fmt.Errorf("atomicfile: sync dir: %w", err)
	}

	return nil
}

// Abort discards the temp file without renaming. The target is untouched.
// Safe to call multiple times or after Close.
func (w *Writer) Abort() error {
	if w.done {
		return nil
	}
	w.done = true
	_ = w.f.Close()
	return os.Remove(w.f.Name())
}
