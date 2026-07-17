package main

import (
	"os"
	"path/filepath"
	"testing"
)

// beads-0p86: hermetic round-trip test for the auto-import stamp helpers
// (autoImportStampPath / writeAutoImportStamp / autoImportStampMatches),
// verified 0% + no test references. All are file-backed under a beadsDir, so a
// t.TempDir plus a real file's os.Stat exercises them without a DB.

func TestAutoImportStampPath(t *testing.T) {
	got := autoImportStampPath("/some/.beads")
	want := filepath.Join("/some/.beads", ".auto-import-issues.jsonl")
	if got != want {
		t.Errorf("autoImportStampPath = %q, want %q", got, want)
	}
}

func TestAutoImportStamp_RoundTrip(t *testing.T) {
	dir := t.TempDir()

	// A source file whose size+mtime the stamp records.
	src := filepath.Join(dir, "issues.jsonl")
	if err := os.WriteFile(src, []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	info, err := os.Stat(src)
	if err != nil {
		t.Fatalf("stat src: %v", err)
	}

	t.Run("no stamp file yet → does not match", func(t *testing.T) {
		if autoImportStampMatches(dir, info) {
			t.Error("expected no match before any stamp is written")
		}
	})

	t.Run("write then match", func(t *testing.T) {
		writeAutoImportStamp(dir, info)
		if _, err := os.Stat(autoImportStampPath(dir)); err != nil {
			t.Fatalf("stamp file not written: %v", err)
		}
		if !autoImportStampMatches(dir, info) {
			t.Error("expected the freshly-written stamp to match the same file info")
		}
	})

	t.Run("changed size → no longer matches", func(t *testing.T) {
		// Append so size (and mtime) differ from the recorded stamp.
		if err := os.WriteFile(src, []byte("one\ntwo\nthree\n"), 0o644); err != nil {
			t.Fatalf("rewrite src: %v", err)
		}
		info2, err := os.Stat(src)
		if err != nil {
			t.Fatalf("stat src2: %v", err)
		}
		if autoImportStampMatches(dir, info2) {
			t.Error("expected mismatch after the source file changed")
		}
	})

	t.Run("corrupt stamp JSON → does not match", func(t *testing.T) {
		if err := os.WriteFile(autoImportStampPath(dir), []byte("{not json"), 0o600); err != nil {
			t.Fatalf("write corrupt stamp: %v", err)
		}
		if autoImportStampMatches(dir, info) {
			t.Error("expected no match when the stamp file is unparseable")
		}
	})
}
