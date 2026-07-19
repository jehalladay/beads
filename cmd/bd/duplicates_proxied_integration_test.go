//go:build cgo

package main

import (
	"strings"
	"testing"
)

// TestProxiedServerDuplicates proves bd duplicates (list path) is
// proxied-server-aware (beads-igmz). Before the fix, duplicates.go called
// store.SearchIssues() with a nil global `store` in proxiedServerMode
// (duplicates is not a noDbCommand) → nil pointer dereference panic on
// hub-connected crew. Clean-mirror leg (zawz/iq3i class): the read path routes
// through the UOW IssueUseCase.SearchIssues; the --auto-merge WRITE path is
// gated off in proxied mode (performMerge needs GetDependentsWithMetadata, not
// yet on the UOW — beads-crys).
func TestProxiedServerDuplicates(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	t.Run("duplicates_list_does_not_nil_panic", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "dup")
		bdProxiedCreate(t, bd, p.dir, "Fix the login flow", "--type", "bug")
		bdProxiedCreate(t, bd, p.dir, "Fix the login flow", "--type", "bug")

		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "duplicates")
		combined := stdout + stderr
		if strings.Contains(combined, "storage is nil") {
			t.Fatalf("bd duplicates hit 'storage is nil' in proxied mode (not proxied-server-aware):\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
		}
		if strings.Contains(combined, "nil pointer dereference") || strings.Contains(combined, "panic:") {
			t.Fatalf("bd duplicates PANICKED in proxied mode (nil store deref):\nerr:%v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}
		if err != nil {
			t.Fatalf("bd duplicates failed in proxied mode: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}
	})

	t.Run("duplicates_json_does_not_nil_panic", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "duj")
		bdProxiedCreate(t, bd, p.dir, "Header renders twice on reload", "--type", "bug")
		bdProxiedCreate(t, bd, p.dir, "Header renders twice on reload", "--type", "bug")

		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "duplicates", "--json")
		combined := stdout + stderr
		if strings.Contains(combined, "storage is nil") || strings.Contains(combined, "nil pointer dereference") || strings.Contains(combined, "panic:") {
			t.Fatalf("bd duplicates --json hit nil store in proxied mode:\nerr:%v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}
		if err != nil {
			t.Fatalf("bd duplicates --json failed in proxied mode: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}
		if !strings.Contains(stdout, "duplicate_groups") {
			t.Errorf("expected \"duplicate_groups\" key in --json output:\n%s", stdout)
		}
	})

	t.Run("auto_merge_rejected_cleanly_not_panic", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "dua")
		bdProxiedCreate(t, bd, p.dir, "Duplicate ticket alpha", "--type", "bug")
		bdProxiedCreate(t, bd, p.dir, "Duplicate ticket alpha", "--type", "bug")

		// --auto-merge is not yet proxied (performMerge needs an un-proxied
		// method). It must reject with a clear message, NOT nil-panic.
		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "duplicates", "--auto-merge")
		combined := stdout + stderr
		if strings.Contains(combined, "nil pointer dereference") || strings.Contains(combined, "panic:") {
			t.Fatalf("bd duplicates --auto-merge PANICKED in proxied mode instead of rejecting cleanly:\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
		}
		if err == nil {
			t.Fatalf("expected bd duplicates --auto-merge to be rejected in proxied mode, got success:\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
		}
		if !strings.Contains(combined, "not yet supported") {
			t.Errorf("expected a clear 'not yet supported' rejection for --auto-merge in proxied mode:\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
		}
	})
}
