package utils

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestResolveForWriteLstatError covers the branch where os.Lstat fails with an
// error other than IsNotExist. Using a regular file as a path *component*
// yields ENOTDIR, which is neither nil nor IsNotExist.
func TestResolveForWriteLstatError(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	file := filepath.Join(tmpDir, "afile")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// "afile/child" treats a regular file as a directory -> ENOTDIR on Lstat.
	badPath := filepath.Join(file, "child")

	got, err := ResolveForWrite(badPath)
	if err == nil {
		t.Fatalf("ResolveForWrite(%q) = %q, want a non-nil error", badPath, got)
	}
	if got != "" {
		t.Errorf("ResolveForWrite error case returned path %q, want empty", got)
	}
}

// TestCanonicalizePathNonExistent covers the EvalSymlinks-fails fallback: a
// non-existent absolute path cannot be symlink-resolved, so CanonicalizePath
// returns the absolute path unchanged.
func TestCanonicalizePathNonExistent(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	missing := filepath.Join(tmpDir, "does-not-exist", "child")

	got := CanonicalizePath(missing)
	if !filepath.IsAbs(got) {
		t.Errorf("CanonicalizePath(%q) = %q, want an absolute path", missing, got)
	}
	if !strings.HasSuffix(got, filepath.Join("does-not-exist", "child")) {
		t.Errorf("CanonicalizePath(%q) = %q, want it to end with the input tail", missing, got)
	}
}

// TestNormalizePathForComparisonNonExistent covers the EvalSymlinks-fails
// fallback in NormalizePathForComparison (uses the absolute path).
func TestNormalizePathForComparisonNonExistent(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	missing := filepath.Join(tmpDir, "nope")

	got := NormalizePathForComparison(missing)
	if !filepath.IsAbs(got) {
		t.Errorf("NormalizePathForComparison(%q) = %q, want an absolute path", missing, got)
	}
}
