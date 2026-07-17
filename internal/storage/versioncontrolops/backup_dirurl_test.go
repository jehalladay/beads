package versioncontrolops

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestDirToFileURL(t *testing.T) {
	t.Parallel()

	t.Run("absolute path is preserved", func(t *testing.T) {
		t.Parallel()
		got, err := DirToFileURL("/var/lib/beads")
		if err != nil {
			t.Fatalf("DirToFileURL returned error: %v", err)
		}
		if got != "file:///var/lib/beads" {
			t.Errorf("DirToFileURL(/var/lib/beads) = %q, want file:///var/lib/beads", got)
		}
	})

	t.Run("relative path is resolved to absolute", func(t *testing.T) {
		t.Parallel()
		got, err := DirToFileURL("relative/dir")
		if err != nil {
			t.Fatalf("DirToFileURL returned error: %v", err)
		}
		// The result must be a file:// URL whose path is absolute and ends
		// with the input segments.
		if !strings.HasPrefix(got, "file://") {
			t.Errorf("result %q missing file:// prefix", got)
		}
		path := strings.TrimPrefix(got, "file://")
		if !filepath.IsAbs(path) {
			t.Errorf("resolved path %q is not absolute", path)
		}
		if !strings.HasSuffix(path, filepath.Join("relative", "dir")) {
			t.Errorf("resolved path %q does not end with the input dir", path)
		}
	})

	t.Run("dot resolves to the working directory", func(t *testing.T) {
		t.Parallel()
		got, err := DirToFileURL(".")
		if err != nil {
			t.Fatalf("DirToFileURL(.) returned error: %v", err)
		}
		wd, err := filepath.Abs(".")
		if err != nil {
			t.Fatalf("filepath.Abs(.): %v", err)
		}
		if got != "file://"+wd {
			t.Errorf("DirToFileURL(.) = %q, want file://%s", got, wd)
		}
	})
}
